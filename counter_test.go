package gloom

import (
	"sync"
	"testing"
	"unsafe"
)

// TestStripedCounterExact verifies the striped counter returns the exact total after
// concurrent increments, and that the stripe index has no effect on the count.
func TestStripedCounterExact(t *testing.T) {
	c := newStripedCounter()

	const goroutines = 16
	const perG = 100_000
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			for i := range uint64(perG) {
				// Vary the stripe index widely, including values that alias the same
				// stripe, to exercise both contended and uncontended paths.
				c.add(seed*0x9e3779b97f4a7c15 + i)
			}
		}(uint64(g))
	}
	wg.Wait()

	if got, want := c.load(), uint64(goroutines*perG); got != want {
		t.Errorf("striped counter total = %d, want %d", got, want)
	}
}

// TestStripedCounterStripeAlignment verifies each stripe occupies a full, separate cache
// line so increments never falsely share a line with an adjacent stripe.
func TestStripedCounterStripeAlignment(t *testing.T) {
	if got := unsafe.Sizeof(paddedCounter{}); got != cacheLineSize {
		t.Fatalf("paddedCounter size = %d, want %d (full cache line)", got, cacheLineSize)
	}

	c := newStripedCounter()
	if len(c.stripes) < 1 {
		t.Fatal("expected at least one stripe")
	}
	// Stripe count must be a power of two so mask-based indexing is unbiased.
	if n := uint64(len(c.stripes)); n&(n-1) != 0 {
		t.Errorf("stripe count %d is not a power of two", n)
	}
	if c.mask != uint64(len(c.stripes))-1 {
		t.Errorf("mask = %d, want %d", c.mask, len(c.stripes)-1)
	}
	if len(c.stripes) > maxCounterStripes {
		t.Errorf("stripe count %d exceeds cap %d", len(c.stripes), maxCounterStripes)
	}

	// Adjacent stripes must start exactly one cache line apart.
	if len(c.stripes) >= 2 {
		a := uintptr(unsafe.Pointer(&c.stripes[0]))
		b := uintptr(unsafe.Pointer(&c.stripes[1]))
		if b-a != cacheLineSize {
			t.Errorf("adjacent stripe distance = %d, want %d", b-a, cacheLineSize)
		}
	}
}

// TestAtomicFilterCountExactConcurrent is an end-to-end check that AtomicFilter.Count
// remains exact after heavy concurrent Add, now that the counter is striped.
func TestAtomicFilterCountExactConcurrent(t *testing.T) {
	f := NewAtomic(2_000_000, 0.01)

	const goroutines = 16
	const perG = 50_000
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range perG {
				f.AddString(fmtKey(id, i))
			}
		}(g)
	}
	wg.Wait()

	if got, want := f.Count(), uint64(goroutines*perG); got != want {
		t.Errorf("Count() = %d, want %d", got, want)
	}
}

func fmtKey(id, i int) string {
	// Small helper kept allocation-light and deterministic.
	var b [32]byte
	n := 0
	n += copyInt(b[n:], id)
	b[n] = '-'
	n++
	n += copyInt(b[n:], i)
	return string(b[:n])
}

func copyInt(dst []byte, v int) int {
	if v == 0 {
		dst[0] = '0'
		return 1
	}
	var tmp [20]byte
	n := 0
	for v > 0 {
		tmp[n] = byte('0' + v%10)
		v /= 10
		n++
	}
	for i := range n {
		dst[i] = tmp[n-1-i]
	}
	return n
}
