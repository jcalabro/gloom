package gloom

import (
	"fmt"
	"testing"
)

// bloomFilterImpls returns a fresh instance of every BloomFilter implementation.
func bloomFilterImpls() map[string]BloomFilter {
	return map[string]BloomFilter{
		"Filter":              New(10_000, 0.01),
		"AtomicFilter":        NewAtomic(10_000, 0.01),
		"ShardedAtomicFilter": NewShardedAtomic(10_000, 0.01, 4),
	}
}

func TestBloomFilterInterface(t *testing.T) {
	for name, f := range bloomFilterImpls() {
		t.Run(name, func(t *testing.T) {
			if f.Count() != 0 {
				t.Errorf("Count() = %d on empty filter, want 0", f.Count())
			}

			if f.EstimatedFillRatio() != 0 {
				t.Errorf("EstimatedFillRatio() = %f on empty filter, want 0", f.EstimatedFillRatio())
			}

			if f.SampledFillRatio(0) != 0 {
				t.Errorf("SampledFillRatio(0) = %f on empty filter, want 0", f.SampledFillRatio(0))
			}

			if f.EstimatedFalsePositiveRate() != 0 {
				t.Errorf("EstimatedFalsePositiveRate() = %f on empty filter, want 0", f.EstimatedFalsePositiveRate())
			}

			if f.TestString("absent") {
				t.Error("TestString on empty filter returned true")
			}

			const n = 1000
			for i := range n {
				if i%2 == 0 {
					f.Add(fmt.Appendf(nil, "item-%d", i))
				} else {
					f.AddString(fmt.Sprintf("item-%d", i))
				}
			}

			// []byte and string variants must hash identically, so each item is
			// visible through both regardless of which method added it.
			for i := range n {
				s := fmt.Sprintf("item-%d", i)
				if !f.Test([]byte(s)) {
					t.Errorf("Test(%q) = false, want true", s)
				}

				if !f.TestString(s) {
					t.Errorf("TestString(%q) = false, want true", s)
				}
			}

			if f.Count() != n {
				t.Errorf("Count() = %d, want %d", f.Count(), n)
			}

			if f.K() == 0 {
				t.Error("K() = 0, want > 0")
			}

			if f.NumBlocks() == 0 {
				t.Error("NumBlocks() = 0, want > 0")
			}

			if f.Cap() != f.NumBlocks()*BlockBits {
				t.Errorf("Cap() = %d, want NumBlocks()*BlockBits = %d", f.Cap(), f.NumBlocks()*BlockBits)
			}

			if r := f.EstimatedFillRatio(); r <= 0 || r >= 1 {
				t.Errorf("EstimatedFillRatio() = %f, want in (0, 1)", r)
			}

			if r := f.SampledFillRatio(0); r != f.EstimatedFillRatio() {
				t.Errorf("SampledFillRatio(0) = %f, want full scan result %f", r, f.EstimatedFillRatio())
			}

			if r := f.EstimatedFalsePositiveRate(); r <= 0 || r >= 1 {
				t.Errorf("EstimatedFalsePositiveRate() = %f, want in (0, 1)", r)
			}
		})
	}
}
