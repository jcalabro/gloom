package gloom

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

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
	for i := uint64(0); i < expectedItems; i++ {
		f.Add([]byte(fmt.Sprintf("item-%d", i)))
	}

	// Test with items not in the filter
	testItems := uint64(10000)
	var falsePositives uint64
	for i := uint64(0); i < testItems; i++ {
		if f.Test([]byte(fmt.Sprintf("notitem-%d", i))) {
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
	for i := 0; i < 500; i++ {
		f.Add([]byte(fmt.Sprintf("item-%d", i)))
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

	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < itemsPerGoroutine; i++ {
				key := fmt.Sprintf("g%d-item-%d", goroutineID, i)
				f.AddString(key)
			}
		}(g)
	}

	wg.Wait()

	// Verify all items are present
	var missing int
	for g := 0; g < numGoroutines; g++ {
		for i := 0; i < itemsPerGoroutine; i++ {
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
	for i := 0; i < 1000; i++ {
		f.AddString(fmt.Sprintf("prepop-%d", i))
	}

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // writers and readers

	// Writers
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				f.AddString(fmt.Sprintf("write-g%d-%d", goroutineID, i))
			}
		}(g)
	}

	// Readers
	for g := 0; g < numGoroutines; g++ {
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				// Test prepopulated items (should always be present)
				f.TestString(fmt.Sprintf("prepop-%d", i%1000))
				// Test items being written (may or may not be present)
				f.TestString(fmt.Sprintf("write-g%d-%d", goroutineID, i))
			}
		}(g)
	}

	wg.Wait()

	// Verify prepopulated items are still present
	for i := 0; i < 1000; i++ {
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
		for i := 0; i < 1000; i++ {
			f.AddString(fmt.Sprintf("item-%d", i))
		}

		// Verify they're present
		var missing int
		for i := 0; i < 1000; i++ {
			if !f.TestString(fmt.Sprintf("item-%d", i)) {
				missing++
			}
		}

		if missing > 0 {
			t.Errorf("k=%d: %d items missing", k, missing)
		}
	}
}
