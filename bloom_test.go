package gloom

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"unsafe"
)

// unsafePointer returns the unsafe.Pointer for a value.
// Used in tests to verify cache-line alignment.
func unsafePointer[T any](v *T) unsafe.Pointer {
	return unsafe.Pointer(v)
}

func TestFilterBasic(t *testing.T) {
	f := New(1000, 0.01)

	// Test adding and checking
	f.Add([]byte("hello"))
	f.Add([]byte("world"))
	f.AddString("foo")

	if !f.Test([]byte("hello")) {
		t.Error("expected hello to be present")
	}
	if !f.Test([]byte("world")) {
		t.Error("expected world to be present")
	}
	if !f.TestString("foo") {
		t.Error("expected foo to be present")
	}

	// These should definitely not be present (with high probability)
	if f.Test([]byte("notpresent")) {
		t.Log("warning: false positive for 'notpresent'")
	}
}

func TestFilterFalsePositiveRate(t *testing.T) {
	expectedItems := uint64(10000)
	targetFPRate := 0.01 // 1%

	f := New(expectedItems, targetFPRate)

	// Add expectedItems
	for i := range expectedItems {
		f.Add(fmt.Appendf(nil, "item-%d", i))
	}

	// Test with items not in the filter
	testItems := uint64(10000)
	var falsePositives uint64
	for i := range testItems {
		if f.Test(fmt.Appendf(nil, "notitem-%d", i)) {
			falsePositives++
		}
	}

	actualFPRate := float64(falsePositives) / float64(testItems)

	// Allow 2x margin for statistical variance
	if actualFPRate > targetFPRate*2 {
		t.Errorf("false positive rate too high: got %.4f, want <= %.4f", actualFPRate, targetFPRate*2)
	}

	t.Logf("FP rate: %.4f (target: %.4f, k=%d, blocks=%d)", actualFPRate, targetFPRate, f.K(), f.NumBlocks())
}

func TestFilterEstimatedFillRatio(t *testing.T) {
	f := New(1000, 0.01)

	// Empty filter should have 0 fill ratio
	if f.EstimatedFillRatio() != 0 {
		t.Errorf("expected 0 fill ratio for empty filter, got %f", f.EstimatedFillRatio())
	}

	// Add some items
	for i := range 500 {
		f.Add(fmt.Appendf(nil, "item-%d", i))
	}

	ratio := f.EstimatedFillRatio()
	if ratio <= 0 || ratio >= 1 {
		t.Errorf("expected fill ratio between 0 and 1, got %f", ratio)
	}

	t.Logf("Fill ratio after 500 items: %.4f", ratio)
}

func TestAtomicFilterBasic(t *testing.T) {
	f := NewAtomic(1000, 0.01)

	f.Add([]byte("hello"))
	f.Add([]byte("world"))
	f.AddString("foo")

	if !f.Test([]byte("hello")) {
		t.Error("expected hello to be present")
	}
	if !f.Test([]byte("world")) {
		t.Error("expected world to be present")
	}
	if !f.TestString("foo") {
		t.Error("expected foo to be present")
	}
}

func TestAtomicFilterConcurrent(t *testing.T) {
	f := NewAtomic(100000, 0.01)

	const numGoroutines = 8
	const itemsPerGoroutine = 10000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(goroutineID int) {
			defer wg.Done()
			for i := range itemsPerGoroutine {
				key := fmt.Sprintf("g%d-item-%d", goroutineID, i)
				f.AddString(key)
			}
		}(g)
	}

	wg.Wait()

	// Verify all items are present
	var missing int
	for g := range numGoroutines {
		for i := range itemsPerGoroutine {
			key := fmt.Sprintf("g%d-item-%d", g, i)
			if !f.TestString(key) {
				missing++
			}
		}
	}

	if missing > 0 {
		t.Errorf("expected all items to be present, but %d were missing", missing)
	}

	expectedCount := uint64(numGoroutines * itemsPerGoroutine)
	if f.Count() != expectedCount {
		t.Errorf("expected count %d, got %d", expectedCount, f.Count())
	}
}

func TestAtomicFilterConcurrentMixed(t *testing.T) {
	f := NewAtomic(100000, 0.01)

	const numGoroutines = 8
	const opsPerGoroutine = 10000

	// Pre-populate with some items
	for i := range 1000 {
		f.AddString(fmt.Sprintf("prepop-%d", i))
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // writers and readers

	// Writers
	for g := range numGoroutines {
		go func(goroutineID int) {
			defer wg.Done()
			for i := range opsPerGoroutine {
				f.AddString(fmt.Sprintf("write-g%d-%d", goroutineID, i))
			}
		}(g)
	}

	// Readers
	for g := range numGoroutines {
		go func(goroutineID int) {
			defer wg.Done()
			for i := range opsPerGoroutine {
				// Test prepopulated items (should always be present)
				f.TestString(fmt.Sprintf("prepop-%d", i%1000))
				// Test items being written (may or may not be present)
				f.TestString(fmt.Sprintf("write-g%d-%d", goroutineID, i))
			}
		}(g)
	}

	wg.Wait()

	// Verify prepopulated items are still present
	for i := range 1000 {
		if !f.TestString(fmt.Sprintf("prepop-%d", i)) {
			t.Errorf("prepopulated item %d missing", i)
		}
	}
}

func TestOptimalParams(t *testing.T) {
	tests := []struct {
		items  uint64
		fpRate float64
		wantK  uint32
	}{
		{1000, 0.01, 7},      // 1% FP rate -> k~7
		{10000, 0.001, 10},   // 0.1% FP rate -> k~10
		{100000, 0.0001, 13}, // 0.01% FP rate -> k~13
	}

	for _, tt := range tests {
		numBlocks, k, bpi := OptimalParams(tt.items, tt.fpRate)
		t.Logf("items=%d, fpRate=%.4f -> numBlocks=%d, k=%d, bitsPerItem=%.2f",
			tt.items, tt.fpRate, numBlocks, k, bpi)

		// k should be in reasonable range
		if k < 3 || k > 14 {
			t.Errorf("k=%d out of supported range [3,14]", k)
		}
	}
}

func TestPrimePartitions(t *testing.T) {
	for k := uint32(3); k <= 14; k++ {
		primes := GetPrimePartition(k)
		if primes == nil {
			t.Errorf("no partition for k=%d", k)
			continue
		}

		if uint32(len(primes)) != k {
			t.Errorf("k=%d: expected %d primes, got %d", k, k, len(primes))
		}

		var sum uint32
		for _, p := range primes {
			sum += p
		}

		// Sum should be close to 512
		if sum < 500 || sum > 520 {
			t.Errorf("k=%d: prime sum=%d, expected ~512", k, sum)
		}

		t.Logf("k=%d: primes=%v, sum=%d", k, primes, sum)
	}
}

func TestEstimateFalsePositiveRate(t *testing.T) {
	// Test against known formula
	numBlocks := uint64(100)
	k := uint32(7)
	items := uint64(5000)

	estimated := EstimateFalsePositiveRate(numBlocks, k, items)

	// Manual calculation: (1 - e^(-kn/m))^k
	m := float64(numBlocks * BlockBits)
	n := float64(items)
	kf := float64(k)
	expected := math.Pow(1-math.Exp(-kf*n/m), kf)

	if math.Abs(estimated-expected) > 0.0001 {
		t.Errorf("estimated=%f, expected=%f", estimated, expected)
	}
}

func TestFilterWithDifferentKValues(t *testing.T) {
	for k := uint32(3); k <= 14; k++ {
		f := NewWithParams(100, k)

		// Add some items
		for i := range 1000 {
			f.AddString(fmt.Sprintf("item-%d", i))
		}

		// Verify they're present
		var missing int
		for i := range 1000 {
			if !f.TestString(fmt.Sprintf("item-%d", i)) {
				missing++
			}
		}

		if missing > 0 {
			t.Errorf("k=%d: %d items missing", k, missing)
		}
	}
}

func TestShardedAtomicFilterBasic(t *testing.T) {
	f := NewShardedAtomic(1000, 0.01, 4)

	f.Add([]byte("hello"))
	f.Add([]byte("world"))
	f.AddString("foo")

	if !f.Test([]byte("hello")) {
		t.Error("expected hello to be present")
	}
	if !f.Test([]byte("world")) {
		t.Error("expected world to be present")
	}
	if !f.TestString("foo") {
		t.Error("expected foo to be present")
	}

	if f.NumShards() != 4 {
		t.Errorf("expected 4 shards, got %d", f.NumShards())
	}
}

func TestShardedAtomicFilterConcurrent(t *testing.T) {
	f := NewShardedAtomic(100000, 0.01, 16)

	const numGoroutines = 8
	const itemsPerGoroutine = 10000

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(goroutineID int) {
			defer wg.Done()
			for i := range itemsPerGoroutine {
				key := fmt.Sprintf("g%d-item-%d", goroutineID, i)
				f.AddString(key)
			}
		}(g)
	}

	wg.Wait()

	// Verify all items are present
	var missing int
	for g := range numGoroutines {
		for i := range itemsPerGoroutine {
			key := fmt.Sprintf("g%d-item-%d", g, i)
			if !f.TestString(key) {
				missing++
			}
		}
	}

	if missing > 0 {
		t.Errorf("expected all items to be present, but %d were missing", missing)
	}

	expectedCount := uint64(numGoroutines * itemsPerGoroutine)
	if f.Count() != expectedCount {
		t.Errorf("expected count %d, got %d", expectedCount, f.Count())
	}
}

func TestNextPowerOf2(t *testing.T) {
	tests := []struct {
		input    uint64
		expected uint64
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{9, 16},
		{15, 16},
		{16, 16},
		{17, 32},
	}

	for _, tt := range tests {
		result := nextPowerOf2(tt.input)
		if result != tt.expected {
			t.Errorf("nextPowerOf2(%d) = %d, want %d", tt.input, result, tt.expected)
		}
	}
}

// Additional coverage tests

func TestFilterCap(t *testing.T) {
	f := New(1000, 0.01)
	cap := f.Cap()
	if cap == 0 {
		t.Error("expected non-zero capacity")
	}
	if cap != f.NumBlocks()*BlockBits {
		t.Errorf("Cap() = %d, want %d", cap, f.NumBlocks()*BlockBits)
	}
}

func TestFilterEstimatedFalsePositiveRate(t *testing.T) {
	f := New(1000, 0.01)

	// Empty filter should have 0 FP rate
	if f.EstimatedFalsePositiveRate() != 0 {
		t.Error("expected 0 FP rate for empty filter")
	}

	// Add some items
	for i := range 500 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	fpRate := f.EstimatedFalsePositiveRate()
	if fpRate <= 0 || fpRate >= 1 {
		t.Errorf("expected FP rate between 0 and 1, got %f", fpRate)
	}
}

func TestAtomicFilterCap(t *testing.T) {
	f := NewAtomic(1000, 0.01)
	cap := f.Cap()
	if cap == 0 {
		t.Error("expected non-zero capacity")
	}
	if cap != f.NumBlocks()*BlockBits {
		t.Errorf("Cap() = %d, want %d", cap, f.NumBlocks()*BlockBits)
	}
}

func TestAtomicFilterK(t *testing.T) {
	f := NewAtomic(1000, 0.01)
	k := f.K()
	if k < 3 || k > 14 {
		t.Errorf("K() = %d, expected between 3 and 14", k)
	}
}

func TestAtomicFilterNumBlocks(t *testing.T) {
	f := NewAtomic(1000, 0.01)
	if f.NumBlocks() == 0 {
		t.Error("expected non-zero NumBlocks")
	}
}

func TestAtomicFilterEstimatedFillRatio(t *testing.T) {
	f := NewAtomic(1000, 0.01)

	if f.EstimatedFillRatio() != 0 {
		t.Error("expected 0 fill ratio for empty filter")
	}

	for i := range 500 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	ratio := f.EstimatedFillRatio()
	if ratio <= 0 || ratio >= 1 {
		t.Errorf("expected fill ratio between 0 and 1, got %f", ratio)
	}
}

func TestAtomicFilterEstimatedFalsePositiveRate(t *testing.T) {
	f := NewAtomic(1000, 0.01)

	if f.EstimatedFalsePositiveRate() != 0 {
		t.Error("expected 0 FP rate for empty filter")
	}

	for i := range 500 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	fpRate := f.EstimatedFalsePositiveRate()
	if fpRate <= 0 || fpRate >= 1 {
		t.Errorf("expected FP rate between 0 and 1, got %f", fpRate)
	}
}

func TestShardedAtomicFilterCap(t *testing.T) {
	f := NewShardedAtomic(1000, 0.01, 4)
	cap := f.Cap()
	if cap == 0 {
		t.Error("expected non-zero capacity")
	}
}

func TestShardedAtomicFilterK(t *testing.T) {
	f := NewShardedAtomic(1000, 0.01, 4)
	k := f.K()
	if k < 3 || k > 14 {
		t.Errorf("K() = %d, expected between 3 and 14", k)
	}
}

func TestShardedAtomicFilterNumBlocks(t *testing.T) {
	f := NewShardedAtomic(1000, 0.01, 4)
	if f.NumBlocks() == 0 {
		t.Error("expected non-zero NumBlocks")
	}
}

func TestShardedAtomicFilterEstimatedFillRatio(t *testing.T) {
	f := NewShardedAtomic(1000, 0.01, 4)

	if f.EstimatedFillRatio() != 0 {
		t.Error("expected 0 fill ratio for empty filter")
	}

	for i := range 500 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	ratio := f.EstimatedFillRatio()
	if ratio <= 0 || ratio >= 1 {
		t.Errorf("expected fill ratio between 0 and 1, got %f", ratio)
	}
}

func TestShardedAtomicFilterEstimatedFalsePositiveRate(t *testing.T) {
	f := NewShardedAtomic(1000, 0.01, 4)

	// Add some items
	for i := range 500 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	fpRate := f.EstimatedFalsePositiveRate()
	if fpRate < 0 || fpRate >= 1 {
		t.Errorf("expected FP rate between 0 and 1, got %f", fpRate)
	}
}

func TestNewWithParamsInvalidK(t *testing.T) {
	// Test with unsupported k value (should fall back to k=7)
	f := NewWithParams(100, 99)
	if f.K() != 7 {
		t.Errorf("expected k=7 fallback, got k=%d", f.K())
	}
}

func TestNewAtomicWithParamsInvalidK(t *testing.T) {
	// Test with unsupported k value (should fall back to k=7)
	f := NewAtomicWithParams(100, 99)
	if f.K() != 7 {
		t.Errorf("expected k=7 fallback, got k=%d", f.K())
	}
}

func TestNewWithParamsZeroBlocks(t *testing.T) {
	// Test with 0 blocks (should default to 1)
	f := NewWithParams(0, 7)
	if f.NumBlocks() != 1 {
		t.Errorf("expected 1 block, got %d", f.NumBlocks())
	}
}

func TestNewAtomicWithParamsZeroBlocks(t *testing.T) {
	// Test with 0 blocks (should default to 1)
	f := NewAtomicWithParams(0, 7)
	if f.NumBlocks() != 1 {
		t.Errorf("expected 1 block, got %d", f.NumBlocks())
	}
}

func TestOptimalParamsEdgeCases(t *testing.T) {
	// Test with 0 items (should default to 1)
	numBlocks, k, _ := OptimalParams(0, 0.01)
	if numBlocks == 0 || k == 0 {
		t.Error("expected non-zero params for 0 items")
	}

	// Test with very small items
	numBlocks, k, _ = OptimalParams(1, 0.01)
	if numBlocks == 0 || k == 0 {
		t.Error("expected non-zero params for 1 item")
	}

	// Test with very low FP rate (should cap k at 14)
	_, k, _ = OptimalParams(1000, 0.0000001)
	if k > 14 {
		t.Errorf("expected k <= 14, got %d", k)
	}

	// Test with very high FP rate (should have low k, clamped to 3)
	_, k, _ = OptimalParams(1000, 0.5)
	if k < 3 {
		t.Errorf("expected k >= 3, got %d", k)
	}

	// Test with fpRate <= 0 (should default to 0.0001)
	numBlocks, k, _ = OptimalParams(1000, 0)
	if numBlocks == 0 || k == 0 {
		t.Error("expected non-zero params for fpRate=0")
	}

	numBlocks, k, _ = OptimalParams(1000, -0.1)
	if numBlocks == 0 || k == 0 {
		t.Error("expected non-zero params for negative fpRate")
	}

	// Test with fpRate >= 1 (should default to 0.99)
	numBlocks, k, _ = OptimalParams(1000, 1.0)
	if numBlocks == 0 || k == 0 {
		t.Error("expected non-zero params for fpRate=1.0")
	}

	numBlocks, k, _ = OptimalParams(1000, 2.0)
	if numBlocks == 0 || k == 0 {
		t.Error("expected non-zero params for fpRate>1")
	}
}

func TestEstimateFalsePositiveRateEdgeCases(t *testing.T) {
	// Test with 0 items
	rate := EstimateFalsePositiveRate(100, 7, 0)
	if rate != 0 {
		t.Errorf("expected 0 FP rate for 0 items, got %f", rate)
	}

	// Test with 0 blocks (returns 0 due to early exit)
	rate = EstimateFalsePositiveRate(0, 7, 1000)
	if rate != 0 {
		t.Errorf("expected 0 FP rate for 0 blocks, got %f", rate)
	}
}

func TestGetPrimePartitionInvalid(t *testing.T) {
	// Test with invalid k values
	if GetPrimePartition(0) != nil {
		t.Error("expected nil for k=0")
	}
	if GetPrimePartition(2) != nil {
		t.Error("expected nil for k=2")
	}
	if GetPrimePartition(15) != nil {
		t.Error("expected nil for k=15")
	}
}

func TestCacheLineAlignment(t *testing.T) {
	// Test that Filter blocks are cache-line aligned
	f := New(1000, 0.01)
	addr := uintptr(unsafePointer(&f.blocks[0]))
	if addr%64 != 0 {
		t.Errorf("Filter blocks not 64-byte aligned: address %x", addr)
	}

	// Test that AtomicFilter blocks are cache-line aligned
	af := NewAtomic(1000, 0.01)
	addrAtomic := uintptr(unsafePointer(&af.blocks[0]))
	if addrAtomic%64 != 0 {
		t.Errorf("AtomicFilter blocks not 64-byte aligned: address %x", addrAtomic)
	}

	// Test that ShardedAtomicFilter shards are cache-line aligned
	sf := NewShardedAtomic(1000, 0.01, 4)
	for i, shard := range sf.shards {
		addrShard := uintptr(unsafePointer(&shard.blocks[0]))
		if addrShard%64 != 0 {
			t.Errorf("ShardedAtomicFilter shard %d not 64-byte aligned: address %x", i, addrShard)
		}
	}
}

// =============================================================================
// Fuzz Tests - No False Negatives Property
// =============================================================================
//
// These fuzz tests verify the fundamental bloom filter invariant: if an item
// has been added to the filter, Test() must return true. A false negative
// would indicate a serious bug in the implementation.
//
// Run with: go test -fuzz=FuzzFilter -fuzztime=30s

func FuzzFilterNoFalseNegatives(f *testing.F) {
	// Seed corpus with various byte patterns
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte("a]0b\x00c\xffd"))

	filter := New(10000, 0.01)

	f.Fuzz(func(t *testing.T, data []byte) {
		filter.Add(data)
		if !filter.Test(data) {
			t.Errorf("false negative: added %q but Test returned false", data)
		}
	})
}

func FuzzAtomicFilterNoFalseNegatives(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte("a\x00b\xffc"))

	filter := NewAtomic(10000, 0.01)

	f.Fuzz(func(t *testing.T, data []byte) {
		filter.Add(data)
		if !filter.Test(data) {
			t.Errorf("false negative: added %q but Test returned false", data)
		}
	})
}

func FuzzShardedAtomicFilterNoFalseNegatives(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte(""))
	f.Add([]byte{0x00})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Add([]byte("a\x00b\xffc"))

	filter := NewShardedAtomic(10000, 0.01, 16)

	f.Fuzz(func(t *testing.T, data []byte) {
		filter.Add(data)
		if !filter.Test(data) {
			t.Errorf("false negative: added %q but Test returned false", data)
		}
	})
}

// FuzzFilterStringNoFalseNegatives tests the string variants
func FuzzFilterStringNoFalseNegatives(f *testing.F) {
	f.Add("hello")
	f.Add("")
	f.Add("a\x00b\xffc")
	f.Add("unicode: \u0000\u0001\u00ff")

	filter := New(10000, 0.01)

	f.Fuzz(func(t *testing.T, data string) {
		filter.AddString(data)
		if !filter.TestString(data) {
			t.Errorf("false negative: added %q but TestString returned false", data)
		}
	})
}

// =============================================================================
// Statistical Tests - False Positive Rate Bounds
// =============================================================================
//
// These tests verify that the observed false positive rate is within
// statistically expected bounds at various load factors. We use a confidence
// interval approach: the observed rate should be within ~3 standard deviations
// of the expected rate (99.7% confidence).

func TestFalsePositiveRateUnderLoad(t *testing.T) {
	testCases := []struct {
		name       string
		capacity   uint64
		targetFP   float64
		loadFactor float64 // fraction of capacity to fill
	}{
		{"10% load", 10000, 0.01, 0.10},
		{"50% load", 10000, 0.01, 0.50},
		{"100% load", 10000, 0.01, 1.00},
		{"150% load (overfilled)", 10000, 0.01, 1.50},
		{"low FP rate", 10000, 0.001, 1.00},
		{"high FP rate", 10000, 0.05, 1.00},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("Filter", func(t *testing.T) {
				testFilterFPRate(t, tc.capacity, tc.targetFP, tc.loadFactor)
			})
			t.Run("AtomicFilter", func(t *testing.T) {
				testAtomicFilterFPRate(t, tc.capacity, tc.targetFP, tc.loadFactor)
			})
			t.Run("ShardedAtomicFilter", func(t *testing.T) {
				testShardedAtomicFilterFPRate(t, tc.capacity, tc.targetFP, tc.loadFactor)
			})
		})
	}
}

func testFilterFPRate(t *testing.T, capacity uint64, targetFP, loadFactor float64) {
	f := New(capacity, targetFP)
	itemsToAdd := uint64(float64(capacity) * loadFactor)

	// Add items
	for i := range itemsToAdd {
		f.Add(fmt.Appendf(nil, "item-%d", i))
	}

	// Verify no false negatives
	for i := range itemsToAdd {
		if !f.Test(fmt.Appendf(nil, "item-%d", i)) {
			t.Fatalf("false negative at item %d", i)
		}
	}

	// Measure false positive rate with items NOT in the filter
	testItems := uint64(10000)
	var falsePositives uint64
	for i := range testItems {
		if f.Test(fmt.Appendf(nil, "notitem-%d", i)) {
			falsePositives++
		}
	}

	observedFP := float64(falsePositives) / float64(testItems)
	validateFPRate(t, observedFP, targetFP, loadFactor, testItems)
}

func testAtomicFilterFPRate(t *testing.T, capacity uint64, targetFP, loadFactor float64) {
	f := NewAtomic(capacity, targetFP)
	itemsToAdd := uint64(float64(capacity) * loadFactor)

	for i := range itemsToAdd {
		f.Add(fmt.Appendf(nil, "item-%d", i))
	}

	for i := range itemsToAdd {
		if !f.Test(fmt.Appendf(nil, "item-%d", i)) {
			t.Fatalf("false negative at item %d", i)
		}
	}

	testItems := uint64(10000)
	var falsePositives uint64
	for i := range testItems {
		if f.Test(fmt.Appendf(nil, "notitem-%d", i)) {
			falsePositives++
		}
	}

	observedFP := float64(falsePositives) / float64(testItems)
	validateFPRate(t, observedFP, targetFP, loadFactor, testItems)
}

func testShardedAtomicFilterFPRate(t *testing.T, capacity uint64, targetFP, loadFactor float64) {
	f := NewShardedAtomic(capacity, targetFP, 16)
	itemsToAdd := uint64(float64(capacity) * loadFactor)

	for i := range itemsToAdd {
		f.Add(fmt.Appendf(nil, "item-%d", i))
	}

	for i := range itemsToAdd {
		if !f.Test(fmt.Appendf(nil, "item-%d", i)) {
			t.Fatalf("false negative at item %d", i)
		}
	}

	testItems := uint64(10000)
	var falsePositives uint64
	for i := range testItems {
		if f.Test(fmt.Appendf(nil, "notitem-%d", i)) {
			falsePositives++
		}
	}

	observedFP := float64(falsePositives) / float64(testItems)
	validateFPRate(t, observedFP, targetFP, loadFactor, testItems)
}

// validateFPRate checks if observed FP rate is within statistical bounds
func validateFPRate(t *testing.T, observedFP, targetFP, loadFactor float64, testItems uint64) {
	t.Helper()

	// The target FP rate is calibrated for 100% load. At different load levels,
	// we need to estimate the expected FP rate using the bloom filter formula:
	// FP ≈ (1 - e^(-k*n/m))^k
	//
	// Since we don't have direct access to k, m here, we use an approximation:
	// At load factor L, the expected FP rate scales roughly as targetFP^(1/L) for L<1
	// and targetFP*L^2 for L>1

	var expectedFP float64
	switch {
	case loadFactor <= 0.1:
		// At very low load, FP rate is essentially 0
		expectedFP = 0.0
	case loadFactor < 1.0:
		// FP rate scales roughly with fill ratio raised to power k
		// Since k is typically 7-10, and fill ratio ~ loadFactor,
		// FP ≈ targetFP * loadFactor^k ≈ targetFP * loadFactor^7
		expectedFP = targetFP * math.Pow(loadFactor, 7)
	case loadFactor == 1.0:
		expectedFP = targetFP
	default:
		// When overfilled, FP rate increases as more bits get set
		expectedFP = math.Min(1.0, targetFP*loadFactor*loadFactor)
	}

	// Calculate confidence interval using normal approximation to binomial
	// Standard deviation of sample proportion: sqrt(p*(1-p)/n)
	// Use a reasonable estimate for p in variance calculation
	pForVariance := math.Max(expectedFP, 0.001) // Avoid division issues at very low rates
	stdDev := math.Sqrt(pForVariance * (1 - pForVariance) / float64(testItems))

	// Use 4 standard deviations for 99.99% confidence
	margin := 4 * stdDev

	// For very low expected FP rates, use a minimum margin based on sample size
	// With 10000 samples, we might see 0-2 false positives by chance
	minMargin := 3.0 / float64(testItems) // Allow up to 3 FPs on low-rate tests
	if margin < minMargin {
		margin = minMargin
	}

	// Be more generous for low FP rate targets where relative variance is higher
	// Allow up to 3x the target rate for very low targets
	if targetFP <= 0.01 {
		margin = math.Max(margin, targetFP*2.0)
	}

	// Be more generous with the margin for non-100% load tests
	if loadFactor != 1.0 {
		margin = math.Max(margin, targetFP*0.5) // Allow 50% relative error
	}

	lowerBound := math.Max(0, expectedFP-margin)
	upperBound := expectedFP + margin

	t.Logf("load=%.0f%% observed=%.4f expected=%.4f bounds=[%.4f, %.4f]",
		loadFactor*100, observedFP, expectedFP, lowerBound, upperBound)

	// For overfilled case, we only check upper bound isn't absurdly high
	if loadFactor > 1.0 {
		if observedFP > 0.5 && loadFactor < 2.0 {
			t.Errorf("FP rate too high for load factor: observed %.4f at %.0f%% load",
				observedFP, loadFactor*100)
		}
		return
	}

	if observedFP < lowerBound || observedFP > upperBound {
		t.Errorf("FP rate outside expected bounds: observed %.4f, expected %.4f +/- %.4f",
			observedFP, expectedFP, margin)
	}
}

// TestNoFalseNegativesUnderStress tests that false negatives never occur
// even under heavy load with many items
func TestNoFalseNegativesUnderStress(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	testCases := []struct {
		name     string
		items    uint64
		fpRate   float64
		sharded  bool
		numTests int
	}{
		{"Filter 100k items", 100000, 0.01, false, 3},
		{"AtomicFilter 100k items", 100000, 0.01, false, 3},
		{"ShardedAtomicFilter 100k items", 100000, 0.01, true, 3},
		{"Filter 1M items", 1000000, 0.01, false, 1},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for test := range tc.numTests {
				var addFunc func([]byte)
				var testFunc func([]byte) bool

				if tc.sharded {
					f := NewShardedAtomic(tc.items, tc.fpRate, 16)
					addFunc = f.Add
					testFunc = f.Test
				} else if tc.name[0] == 'A' { // AtomicFilter
					f := NewAtomic(tc.items, tc.fpRate)
					addFunc = f.Add
					testFunc = f.Test
				} else {
					f := New(tc.items, tc.fpRate)
					addFunc = f.Add
					testFunc = f.Test
				}

				// Add all items
				for i := range tc.items {
					addFunc(fmt.Appendf(nil, "stress-test-%d-%d", test, i))
				}

				// Verify all items are found (no false negatives)
				var falseNegatives uint64
				for i := range tc.items {
					if !testFunc(fmt.Appendf(nil, "stress-test-%d-%d", test, i)) {
						falseNegatives++
						if falseNegatives <= 10 {
							t.Errorf("false negative at item %d", i)
						}
					}
				}

				if falseNegatives > 0 {
					t.Fatalf("total false negatives: %d out of %d items", falseNegatives, tc.items)
				}
			}
		})
	}
}

// TestConcurrentNoFalseNegatives verifies no false negatives under concurrent access
func TestConcurrentNoFalseNegatives(t *testing.T) {
	const (
		numGoroutines = 8
		itemsPerGo    = 10000
	)

	t.Run("AtomicFilter", func(t *testing.T) {
		f := NewAtomic(numGoroutines*itemsPerGo, 0.01)
		var wg sync.WaitGroup

		// Concurrently add items
		for g := range numGoroutines {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				for i := range itemsPerGo {
					f.Add(fmt.Appendf(nil, "concurrent-%d-%d", goroutineID, i))
				}
			}(g)
		}
		wg.Wait()

		// Verify all items (single-threaded verification is fine)
		for g := range numGoroutines {
			for i := range itemsPerGo {
				key := fmt.Appendf(nil, "concurrent-%d-%d", g, i)
				if !f.Test(key) {
					t.Errorf("false negative for goroutine %d item %d", g, i)
				}
			}
		}
	})

	t.Run("ShardedAtomicFilter", func(t *testing.T) {
		f := NewShardedAtomic(numGoroutines*itemsPerGo, 0.01, 16)
		var wg sync.WaitGroup

		for g := range numGoroutines {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				for i := range itemsPerGo {
					f.Add(fmt.Appendf(nil, "concurrent-%d-%d", goroutineID, i))
				}
			}(g)
		}
		wg.Wait()

		for g := range numGoroutines {
			for i := range itemsPerGo {
				key := fmt.Appendf(nil, "concurrent-%d-%d", g, i)
				if !f.Test(key) {
					t.Errorf("false negative for goroutine %d item %d", g, i)
				}
			}
		}
	})
}

// =============================================================================
// Concurrent Stress Tests
// =============================================================================

// TestConcurrentStressAtomicFilter tests AtomicFilter under heavy concurrent load
// with mixed reads and writes happening simultaneously.
func TestConcurrentStressAtomicFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const (
		numWriters     = 16
		numReaders     = 16
		itemsPerWriter = 50000
		readsPerReader = 100000
	)

	f := NewAtomic(numWriters*itemsPerWriter, 0.01)
	var wg sync.WaitGroup

	// Track which items have been added (for verification)
	added := make([][]bool, numWriters)
	for i := range added {
		added[i] = make([]bool, itemsPerWriter)
	}

	// Start writers
	for w := range numWriters {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := range itemsPerWriter {
				key := fmt.Appendf(nil, "stress-%d-%d", writerID, i)
				f.Add(key)
				added[writerID][i] = true
			}
		}(w)
	}

	// Start readers that continuously test random keys
	for r := range numReaders {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for i := range readsPerReader {
				// Test a key that might or might not exist
				writerID := (readerID + i) % numWriters
				itemID := i % itemsPerWriter
				key := fmt.Appendf(nil, "stress-%d-%d", writerID, itemID)
				_ = f.Test(key) // Just exercise the read path
			}
		}(r)
	}

	wg.Wait()

	// Verify no false negatives for all added items
	falseNegatives := 0
	for w := range numWriters {
		for i := range itemsPerWriter {
			if added[w][i] {
				key := fmt.Appendf(nil, "stress-%d-%d", w, i)
				if !f.Test(key) {
					falseNegatives++
				}
			}
		}
	}

	if falseNegatives > 0 {
		t.Errorf("found %d false negatives out of %d items", falseNegatives, numWriters*itemsPerWriter)
	}

	// Verify count is correct
	expectedCount := uint64(numWriters * itemsPerWriter)
	if f.Count() != expectedCount {
		t.Errorf("expected count %d, got %d", expectedCount, f.Count())
	}
}

// TestConcurrentStressShardedFilter tests ShardedAtomicFilter under heavy concurrent load.
func TestConcurrentStressShardedFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const (
		numWriters     = 32
		numReaders     = 32
		itemsPerWriter = 25000
		readsPerReader = 50000
	)

	f := NewShardedAtomicDefault(numWriters*itemsPerWriter, 0.01)
	var wg sync.WaitGroup

	// Start writers and readers simultaneously
	for w := range numWriters {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := range itemsPerWriter {
				f.AddString(fmt.Sprintf("sharded-stress-%d-%d", writerID, i))
			}
		}(w)
	}

	for r := range numReaders {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for i := range readsPerReader {
				writerID := (readerID + i) % numWriters
				itemID := i % itemsPerWriter
				_ = f.TestString(fmt.Sprintf("sharded-stress-%d-%d", writerID, itemID))
			}
		}(r)
	}

	wg.Wait()

	// Verify no false negatives
	falseNegatives := 0
	for w := range numWriters {
		for i := range itemsPerWriter {
			if !f.TestString(fmt.Sprintf("sharded-stress-%d-%d", w, i)) {
				falseNegatives++
			}
		}
	}

	if falseNegatives > 0 {
		t.Errorf("found %d false negatives out of %d items", falseNegatives, numWriters*itemsPerWriter)
	}
}

// TestConcurrentStressMethodMix tests calling various methods concurrently.
func TestConcurrentStressMethodMix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	const (
		numGoroutines = 32
		opsPerRoutine = 10000
	)

	f := NewAtomic(numGoroutines*opsPerRoutine, 0.01)
	var wg sync.WaitGroup

	for g := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range opsPerRoutine {
				key := fmt.Sprintf("mix-%d-%d", id, i)

				// Mix of operations
				switch i % 5 {
				case 0, 1, 2:
					f.AddString(key)
				case 3:
					_ = f.TestString(key)
				case 4:
					_ = f.Count()
					_ = f.EstimatedFillRatio()
					_ = f.EstimatedFalsePositiveRate()
				}
			}
		}(g)
	}

	wg.Wait()

	// Basic sanity checks
	if f.Count() == 0 {
		t.Error("expected non-zero count")
	}
	ratio := f.EstimatedFillRatio()
	if ratio < 0 || ratio > 1 {
		t.Errorf("fill ratio out of bounds: %f", ratio)
	}
}

// =============================================================================
// Property-Based Tests
// =============================================================================

// TestPropertyNoFalseNegatives verifies the fundamental bloom filter property:
// if an item was added, Test must return true.
func TestPropertyNoFalseNegatives(t *testing.T) {
	testCases := []struct {
		name   string
		items  int
		fpRate float64
	}{
		{"small_filter", 100, 0.1},
		{"medium_filter", 10000, 0.01},
		{"large_filter", 100000, 0.001},
		{"high_fp_rate", 1000, 0.5},
		{"low_fp_rate", 1000, 0.0001},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			f := New(uint64(tc.items), tc.fpRate)

			// Add items and immediately verify
			for i := range tc.items {
				key := fmt.Appendf(nil, "prop-%d", i)
				f.Add(key)

				// Property: immediately after Add, Test must return true
				if !f.Test(key) {
					t.Errorf("false negative immediately after adding item %d", i)
				}
			}

			// Verify all items again after all adds complete
			for i := range tc.items {
				key := fmt.Appendf(nil, "prop-%d", i)
				if !f.Test(key) {
					t.Errorf("false negative for item %d after all adds", i)
				}
			}
		})
	}
}

// TestPropertyByteStringEquivalence verifies that byte slice and string
// operations are equivalent.
func TestPropertyByteStringEquivalence(t *testing.T) {
	f := New(10000, 0.01)

	testStrings := []string{
		"hello",
		"world",
		"",
		"a",
		"longer string with spaces",
		"unicode: 你好世界 🌍",
		"\x00\x01\x02", // binary data
	}

	for _, s := range testStrings {
		// Add via string, test via both
		f.AddString(s)
		if !f.TestString(s) {
			t.Errorf("TestString failed after AddString for %q", s)
		}
		if !f.Test([]byte(s)) {
			t.Errorf("Test([]byte) failed after AddString for %q", s)
		}
	}

	f2 := New(10000, 0.01)
	for _, s := range testStrings {
		// Add via bytes, test via both
		f2.Add([]byte(s))
		if !f2.Test([]byte(s)) {
			t.Errorf("Test failed after Add for %q", s)
		}
		if !f2.TestString(s) {
			t.Errorf("TestString failed after Add for %q", s)
		}
	}
}

// TestPropertyIdempotence verifies that adding the same item multiple times
// has the same effect as adding it once.
func TestPropertyIdempotence(t *testing.T) {
	f := New(1000, 0.01)

	key := []byte("idempotent-key")

	// Add once
	f.Add(key)
	countAfterFirst := f.Count()
	if !f.Test(key) {
		t.Error("Test failed after first add")
	}

	// Add same key many more times
	for range 100 {
		f.Add(key)
	}

	// Property: Test still returns true
	if !f.Test(key) {
		t.Error("Test failed after multiple adds of same key")
	}

	// Count increases (we're counting adds, not unique items)
	if f.Count() != countAfterFirst+100 {
		t.Errorf("expected count %d, got %d", countAfterFirst+100, f.Count())
	}
}

// TestPropertyCountMonotonicity verifies that Count() never decreases.
func TestPropertyCountMonotonicity(t *testing.T) {
	f := New(10000, 0.01)

	var lastCount uint64
	for i := range 1000 {
		f.Add(fmt.Appendf(nil, "mono-%d", i))
		currentCount := f.Count()

		if currentCount < lastCount {
			t.Errorf("count decreased from %d to %d at iteration %d", lastCount, currentCount, i)
		}
		lastCount = currentCount
	}
}

// TestPropertyFillRatioBounds verifies EstimatedFillRatio is always in [0, 1].
func TestPropertyFillRatioBounds(t *testing.T) {
	f := New(1000, 0.01)

	// Empty filter
	ratio := f.EstimatedFillRatio()
	if ratio != 0 {
		t.Errorf("empty filter should have 0 fill ratio, got %f", ratio)
	}

	// Add items and check bounds
	for i := range 2000 { // Overfill
		f.Add(fmt.Appendf(nil, "fill-%d", i))
		ratio := f.EstimatedFillRatio()
		if ratio < 0 || ratio > 1 {
			t.Errorf("fill ratio out of bounds at iteration %d: %f", i, ratio)
		}
	}
}

// TestPropertyFPRateBounds verifies EstimatedFalsePositiveRate is always in [0, 1].
func TestPropertyFPRateBounds(t *testing.T) {
	f := New(1000, 0.01)

	// Empty filter should have ~0 FP rate
	fpRate := f.EstimatedFalsePositiveRate()
	if fpRate != 0 {
		t.Errorf("empty filter should have 0 FP rate, got %f", fpRate)
	}

	// Add items and check bounds
	for i := range 2000 {
		f.Add(fmt.Appendf(nil, "fp-%d", i))
		fpRate := f.EstimatedFalsePositiveRate()
		if fpRate < 0 || fpRate > 1 {
			t.Errorf("FP rate out of bounds at iteration %d: %f", i, fpRate)
		}
	}
}

// TestPropertyFillRatioMonotonicity verifies fill ratio never decreases.
func TestPropertyFillRatioMonotonicity(t *testing.T) {
	f := New(10000, 0.01)

	var lastRatio float64
	for i := range 5000 {
		f.Add(fmt.Appendf(nil, "ratio-%d", i))
		ratio := f.EstimatedFillRatio()

		if ratio < lastRatio {
			t.Errorf("fill ratio decreased from %f to %f at iteration %d", lastRatio, ratio, i)
		}
		lastRatio = ratio
	}
}

// TestPropertyAtomicFilterEquivalence verifies AtomicFilter behaves the same
// as Filter for single-threaded operations.
func TestPropertyAtomicFilterEquivalence(t *testing.T) {
	f1 := New(10000, 0.01)
	f2 := NewAtomic(10000, 0.01)

	// Same parameters
	if f1.Cap() != f2.Cap() {
		t.Errorf("capacity mismatch: %d vs %d", f1.Cap(), f2.Cap())
	}
	if f1.K() != f2.K() {
		t.Errorf("k mismatch: %d vs %d", f1.K(), f2.K())
	}
	if f1.NumBlocks() != f2.NumBlocks() {
		t.Errorf("numBlocks mismatch: %d vs %d", f1.NumBlocks(), f2.NumBlocks())
	}

	// Add same items to both
	for i := range 1000 {
		key := fmt.Appendf(nil, "equiv-%d", i)
		f1.Add(key)
		f2.Add(key)
	}

	// Test same items - both should return true
	for i := range 1000 {
		key := fmt.Appendf(nil, "equiv-%d", i)
		r1, r2 := f1.Test(key), f2.Test(key)
		if r1 != r2 {
			t.Errorf("result mismatch for item %d: Filter=%v, AtomicFilter=%v", i, r1, r2)
		}
		if !r1 {
			t.Errorf("false negative for item %d", i)
		}
	}

	// Counts should match
	if f1.Count() != f2.Count() {
		t.Errorf("count mismatch: %d vs %d", f1.Count(), f2.Count())
	}
}

// TestPropertyShardedFilterEquivalence verifies ShardedAtomicFilter produces
// correct results (no false negatives).
func TestPropertyShardedFilterEquivalence(t *testing.T) {
	shardCounts := []uint64{1, 2, 4, 8, 16, 32}

	for _, numShards := range shardCounts {
		t.Run(fmt.Sprintf("shards_%d", numShards), func(t *testing.T) {
			f := NewShardedAtomic(10000, 0.01, numShards)

			// Add items
			for i := range 1000 {
				f.AddString(fmt.Sprintf("shard-equiv-%d", i))
			}

			// Verify no false negatives
			for i := range 1000 {
				if !f.TestString(fmt.Sprintf("shard-equiv-%d", i)) {
					t.Errorf("false negative for item %d with %d shards", i, numShards)
				}
			}

			// Verify count
			if f.Count() != 1000 {
				t.Errorf("expected count 1000, got %d with %d shards", f.Count(), numShards)
			}
		})
	}
}

// TestPropertyDifferentKValues verifies the filter works correctly with all
// supported k values.
func TestPropertyDifferentKValues(t *testing.T) {
	for k := uint32(3); k <= 14; k++ {
		t.Run(fmt.Sprintf("k_%d", k), func(t *testing.T) {
			f := NewWithParams(100, k)

			if f.K() != k {
				t.Errorf("expected k=%d, got k=%d", k, f.K())
			}

			// Add and verify items
			for i := range 500 {
				key := fmt.Appendf(nil, "k%d-item-%d", k, i)
				f.Add(key)
				if !f.Test(key) {
					t.Errorf("false negative for k=%d item %d", k, i)
				}
			}
		})
	}
}
