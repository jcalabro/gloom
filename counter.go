package gloom

import (
	"runtime"
	"sync/atomic"
)

// maxCounterStripes bounds the per-filter counter memory. Each stripe occupies a full
// cache line (64 bytes), so the cap keeps the striped counter under 8 KiB even on
// machines with very high GOMAXPROCS.
const maxCounterStripes = 128

// paddedCounter is a single counter occupying an entire cache line. The trailing padding
// ensures that incrementing one stripe never invalidates the cache line of an adjacent
// stripe (false sharing), and that counter writes never invalidate the filter's hot
// read-only metadata, which lives in a separate allocation.
type paddedCounter struct {
	v atomic.Uint64
	_ [cacheLineSize - 8]byte
}

// stripedCounter is a concurrent counter whose increments are spread across many
// cache-line-padded stripes. A single shared atomic counter serializes every concurrent
// Add through one cache line (a measured ~10x throughput cliff under parallel writes);
// striping removes that hot line while keeping the summed total exact.
type stripedCounter struct {
	stripes []paddedCounter
	mask    uint64
}

// newStripedCounter creates a counter with a power-of-two number of stripes derived from
// GOMAXPROCS (the upper bound on concurrent writers), clamped to [1, maxCounterStripes].
// A single-core process gets exactly one stripe and pays no extra memory over a plain
// atomic counter's worth of useful state.
func newStripedCounter() *stripedCounter {
	n := min(nextPowerOf2(uint64(runtime.GOMAXPROCS(0))), maxCounterStripes)
	return &stripedCounter{
		stripes: make([]paddedCounter, n),
		mask:    n - 1,
	}
}

// add increments the counter. idx selects the stripe; callers pass a well-distributed
// value (the block index) so concurrent writers rarely contend on the same stripe.
func (c *stripedCounter) add(idx uint64) {
	c.stripes[idx&c.mask].v.Add(1)
}

// load returns the exact total across all stripes. It is not a linearizable snapshot
// under concurrent writes (stripes are read one at a time), matching the existing
// "approximate under concurrency" contract of Count.
func (c *stripedCounter) load() uint64 {
	var total uint64
	for i := range c.stripes {
		total += c.stripes[i].v.Load()
	}
	return total
}
