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

func TestFilterTestAndAdd(t *testing.T) {
	f := New(1000, 0.01)

	// First add should return false (not present before)
	if f.TestAndAdd([]byte("test")) {
		t.Error("expected TestAndAdd to return false for new item")
	}

	// Second add should return true (was present)
	if !f.TestAndAdd([]byte("test")) {
		t.Error("expected TestAndAdd to return true for existing item")
	}
}

func TestFilterClear(t *testing.T) {
	f := New(100, 0.01)

	f.Add([]byte("test"))
	if !f.Test([]byte("test")) {
		t.Error("expected test to be present before clear")
	}

	f.Clear()

	if f.Test([]byte("test")) {
		t.Error("expected test to not be present after clear")
	}
	if f.Count() != 0 {
		t.Errorf("expected count to be 0 after clear, got %d", f.Count())
	}
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

func TestShardedAtomicFilterTestAndAdd(t *testing.T) {
	f := NewShardedAtomicDefault(1000, 0.01)

	// First add should return false (not present before)
	if f.TestAndAdd([]byte("test")) {
		t.Error("expected TestAndAdd to return false for new item")
	}

	// Second add should return true (was present)
	if !f.TestAndAdd([]byte("test")) {
		t.Error("expected TestAndAdd to return true for existing item")
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

func TestFilterTestAndAddString(t *testing.T) {
	f := New(1000, 0.01)

	if f.TestAndAddString("test") {
		t.Error("expected TestAndAddString to return false for new item")
	}
	if !f.TestAndAddString("test") {
		t.Error("expected TestAndAddString to return true for existing item")
	}
}

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

func TestAtomicFilterTestAndAdd(t *testing.T) {
	f := NewAtomic(1000, 0.01)

	if f.TestAndAdd([]byte("test")) {
		t.Error("expected TestAndAdd to return false for new item")
	}
	if !f.TestAndAdd([]byte("test")) {
		t.Error("expected TestAndAdd to return true for existing item")
	}
}

func TestAtomicFilterTestAndAddString(t *testing.T) {
	f := NewAtomic(1000, 0.01)

	if f.TestAndAddString("test") {
		t.Error("expected TestAndAddString to return false for new item")
	}
	if !f.TestAndAddString("test") {
		t.Error("expected TestAndAddString to return true for existing item")
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

func TestAtomicFilterClear(t *testing.T) {
	f := NewAtomic(100, 0.01)

	f.Add([]byte("test"))
	if !f.Test([]byte("test")) {
		t.Error("expected test to be present before clear")
	}

	f.Clear()

	if f.Test([]byte("test")) {
		t.Error("expected test to not be present after clear")
	}
	if f.Count() != 0 {
		t.Errorf("expected count to be 0 after clear, got %d", f.Count())
	}
}

func TestShardedAtomicFilterTestAndAddString(t *testing.T) {
	f := NewShardedAtomicDefault(1000, 0.01)

	if f.TestAndAddString("test") {
		t.Error("expected TestAndAddString to return false for new item")
	}
	if !f.TestAndAddString("test") {
		t.Error("expected TestAndAddString to return true for existing item")
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

func TestShardedAtomicFilterClear(t *testing.T) {
	f := NewShardedAtomic(100, 0.01, 4)

	f.Add([]byte("test"))
	if !f.Test([]byte("test")) {
		t.Error("expected test to be present before clear")
	}

	f.Clear()

	if f.Test([]byte("test")) {
		t.Error("expected test to not be present after clear")
	}
	if f.Count() != 0 {
		t.Errorf("expected count to be 0 after clear, got %d", f.Count())
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
