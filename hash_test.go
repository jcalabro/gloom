package gloom

import (
	"fmt"
	"math"
	"testing"
)

// TestReduceRange verifies the multiply-shift range reduction stays in bounds and is
// reasonably uniform. This replaces modulo-based block selection, so uniformity here
// directly affects the realized false-positive rate.
func TestReduceRange(t *testing.T) {
	for _, n := range []uint64{1, 2, 3, 7, 1873, 65536, 65537, 1 << 20, (1 << 32) + 1} {
		for _, h := range []uint64{0, 1, math.MaxUint64, math.MaxUint64 / 2, 0x9e3779b97f4a7c15} {
			got := reduceRange(h, n)
			if got >= n {
				t.Errorf("reduceRange(%d, %d) = %d, out of range [0,%d)", h, n, got, n)
			}
		}
		// h = MaxUint64 should map to n-1 (the top of the range).
		if got := reduceRange(math.MaxUint64, n); got != n-1 {
			t.Errorf("reduceRange(MaxUint64, %d) = %d, want %d", n, got, n-1)
		}
		// h = 0 always maps to 0.
		if got := reduceRange(0, n); got != 0 {
			t.Errorf("reduceRange(0, %d) = %d, want 0", n, got)
		}
	}
}

// TestReduceRangeUniformity checks that reduceRange spreads a strong hash evenly across
// buckets, including bucket counts far larger than 2^16 (the count that broke the old
// sharded block-selection scheme). A chi-square statistic guards against gross skew.
func TestReduceRangeUniformity(t *testing.T) {
	for _, n := range []uint64{100_000, 200_000} {
		counts := make([]uint64, n)
		const samples = 20_000_000
		for i := range samples {
			h := hashRawString128(fmt.Sprintf("uniformity-%d", i))
			counts[reduceRange(h.Hi, n)]++
		}
		// Chi-square against uniform expectation.
		expected := float64(samples) / float64(n)
		var chi2 float64
		var zero int
		for _, c := range counts {
			d := float64(c) - expected
			chi2 += d * d / expected
			if c == 0 {
				zero++
			}
		}
		// For df = n-1, chi2 is tightly concentrated around df with stddev sqrt(2*df).
		// Require chi2 within 6 standard deviations of the mean (extremely loose, only
		// catches gross non-uniformity such as dead buckets).
		df := float64(n - 1)
		upper := df + 6*math.Sqrt(2*df)
		if chi2 > upper {
			t.Errorf("n=%d: chi2=%.0f exceeds %.0f (non-uniform distribution)", n, chi2, upper)
		}
		// With ~100-200 samples per bucket expected, NO bucket should be empty. A nonzero
		// count here is exactly the symptom of the old 16-bit cliff (unreachable buckets).
		if zero > 0 {
			t.Errorf("n=%d: %d buckets never selected (dead blocks / range-reduction defect)", n, zero)
		}
	}
}
