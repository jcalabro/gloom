package gloom

import (
	"math/bits"
	"sync/atomic"
)

// Filter is a non-thread-safe bloom filter using cache-line blocked
// one-hashing for optimal performance.
//
// The filter divides memory into 512-bit (64-byte) blocks that fit in a
// single CPU cache line. Each block is partitioned into k segments using
// distinct prime sizes, enabling the one-hashing technique where a single
// hash value generates k independent bit positions via modulo operations.
type Filter struct {
	blocks    []uint64 // 8 uint64s per block = 512 bits
	numBlocks uint64   // Total number of 512-bit blocks
	k         uint32   // Number of hash functions (partitions)
	primes    []uint32 // Prime partition sizes
	offsets   []uint32 // Cumulative offsets within block
	count     uint64   // Number of items added (approximate)
}

// New creates a new bloom filter optimized for the expected number of items
// and desired false positive rate.
func New(expectedItems uint64, fpRate float64) *Filter {
	numBlocks, k, _ := OptimalParams(expectedItems, fpRate)
	return NewWithParams(numBlocks, k)
}

// NewWithParams creates a new bloom filter with explicit parameters.
// numBlocks is the number of 512-bit blocks, k is the number of hash functions.
func NewWithParams(numBlocks uint64, k uint32) *Filter {
	if numBlocks == 0 {
		numBlocks = 1
	}

	primes := GetPrimePartition(k)
	if primes == nil {
		// Default to k=7 if unsupported
		k = 7
		primes = GetPrimePartition(k)
	}

	return &Filter{
		blocks:    make([]uint64, numBlocks*BlockWords),
		numBlocks: numBlocks,
		k:         k,
		primes:    primes,
		offsets:   ComputeOffsets(primes),
	}
}

// Add adds data to the bloom filter.
func (f *Filter) Add(data []byte) {
	blockIdx, intraHash := hashData(data, f.numBlocks)
	f.addWithHash(blockIdx, intraHash)
}

// AddString adds a string to the bloom filter without allocating.
func (f *Filter) AddString(s string) {
	blockIdx, intraHash := hashString(s, f.numBlocks)
	f.addWithHash(blockIdx, intraHash)
}

// addWithHash sets bits in the filter using pre-computed hash values.
func (f *Filter) addWithHash(blockIdx uint64, intraHash uint32) {
	blockBase := blockIdx * BlockWords

	// One-hashing: same hash value mod different primes gives independent positions
	for i := uint32(0); i < f.k; i++ {
		bitPos := f.offsets[i] + (intraHash % f.primes[i])
		wordIdx := bitPos / 64
		bitIdx := bitPos % 64
		f.blocks[blockBase+uint64(wordIdx)] |= (1 << bitIdx)
	}

	f.count++
}

// Test checks if data might be in the bloom filter.
// Returns true if the data might be present (with false positive probability),
// or false if the data is definitely not present.
func (f *Filter) Test(data []byte) bool {
	blockIdx, intraHash := hashData(data, f.numBlocks)
	return f.testWithHash(blockIdx, intraHash)
}

// TestString checks if a string might be in the bloom filter without allocating.
func (f *Filter) TestString(s string) bool {
	blockIdx, intraHash := hashString(s, f.numBlocks)
	return f.testWithHash(blockIdx, intraHash)
}

// testWithHash checks bits in the filter using pre-computed hash values.
func (f *Filter) testWithHash(blockIdx uint64, intraHash uint32) bool {
	blockBase := blockIdx * BlockWords

	for i := uint32(0); i < f.k; i++ {
		bitPos := f.offsets[i] + (intraHash % f.primes[i])
		wordIdx := bitPos / 64
		bitIdx := bitPos % 64
		if f.blocks[blockBase+uint64(wordIdx)]&(1<<bitIdx) == 0 {
			return false
		}
	}

	return true
}

// TestAndAdd tests if data is in the filter, then adds it.
// Returns true if the data might have been present before adding.
func (f *Filter) TestAndAdd(data []byte) bool {
	blockIdx, intraHash := hashData(data, f.numBlocks)
	present := f.testWithHash(blockIdx, intraHash)
	if !present {
		f.addWithHash(blockIdx, intraHash)
	}
	return present
}

// TestAndAddString tests if a string is in the filter, then adds it.
func (f *Filter) TestAndAddString(s string) bool {
	blockIdx, intraHash := hashString(s, f.numBlocks)
	present := f.testWithHash(blockIdx, intraHash)
	if !present {
		f.addWithHash(blockIdx, intraHash)
	}
	return present
}

// Cap returns the capacity of the filter in bits.
func (f *Filter) Cap() uint64 {
	return f.numBlocks * BlockBits
}

// K returns the number of hash functions (partitions) used.
func (f *Filter) K() uint32 {
	return f.k
}

// Count returns the approximate number of items added to the filter.
func (f *Filter) Count() uint64 {
	return f.count
}

// NumBlocks returns the number of 512-bit blocks in the filter.
func (f *Filter) NumBlocks() uint64 {
	return f.numBlocks
}

// EstimatedFillRatio estimates the proportion of bits that are set.
func (f *Filter) EstimatedFillRatio() float64 {
	var setBits uint64
	for _, word := range f.blocks {
		setBits += uint64(bits.OnesCount64(word))
	}
	return float64(setBits) / float64(f.numBlocks*BlockBits)
}

// EstimatedFalsePositiveRate estimates the current false positive rate
// based on the number of items added.
func (f *Filter) EstimatedFalsePositiveRate() float64 {
	return EstimateFalsePositiveRate(f.numBlocks, f.k, f.count)
}

// Clear resets the bloom filter to its initial empty state.
func (f *Filter) Clear() {
	for i := range f.blocks {
		f.blocks[i] = 0
	}
	f.count = 0
}

// AtomicFilter is a thread-safe bloom filter using atomic operations.
// It uses the same cache-line blocked one-hashing technique as Filter
// but with atomic.Uint64 for concurrent access.
type AtomicFilter struct {
	blocks    []atomic.Uint64 // 8 atomic uint64s per block = 512 bits
	numBlocks uint64          // Total number of 512-bit blocks
	k         uint32          // Number of hash functions (partitions)
	primes    []uint32        // Prime partition sizes
	offsets   []uint32        // Cumulative offsets within block
	count     atomic.Uint64   // Number of items added (approximate)
}

// NewAtomic creates a new thread-safe bloom filter optimized for the
// expected number of items and desired false positive rate.
func NewAtomic(expectedItems uint64, fpRate float64) *AtomicFilter {
	numBlocks, k, _ := OptimalParams(expectedItems, fpRate)
	return NewAtomicWithParams(numBlocks, k)
}

// NewAtomicWithParams creates a new thread-safe bloom filter with explicit parameters.
func NewAtomicWithParams(numBlocks uint64, k uint32) *AtomicFilter {
	if numBlocks == 0 {
		numBlocks = 1
	}

	primes := GetPrimePartition(k)
	if primes == nil {
		k = 7
		primes = GetPrimePartition(k)
	}

	return &AtomicFilter{
		blocks:    make([]atomic.Uint64, numBlocks*BlockWords),
		numBlocks: numBlocks,
		k:         k,
		primes:    primes,
		offsets:   ComputeOffsets(primes),
	}
}

// Add adds data to the bloom filter atomically.
func (f *AtomicFilter) Add(data []byte) {
	blockIdx, intraHash := hashData(data, f.numBlocks)
	f.addWithHash(blockIdx, intraHash)
}

// AddString adds a string to the bloom filter atomically without allocating.
func (f *AtomicFilter) AddString(s string) {
	blockIdx, intraHash := hashString(s, f.numBlocks)
	f.addWithHash(blockIdx, intraHash)
}

// addWithHash sets bits atomically using pre-computed hash values.
func (f *AtomicFilter) addWithHash(blockIdx uint64, intraHash uint32) {
	blockBase := blockIdx * BlockWords

	for i := uint32(0); i < f.k; i++ {
		bitPos := f.offsets[i] + (intraHash % f.primes[i])
		wordIdx := bitPos / 64
		bitIdx := bitPos % 64
		mask := uint64(1) << bitIdx
		// Use atomic OR - most efficient on Go 1.23+
		f.blocks[blockBase+uint64(wordIdx)].Or(mask)
	}

	f.count.Add(1)
}

// Test checks if data might be in the bloom filter.
// This operation is safe to call concurrently with Add.
func (f *AtomicFilter) Test(data []byte) bool {
	blockIdx, intraHash := hashData(data, f.numBlocks)
	return f.testWithHash(blockIdx, intraHash)
}

// TestString checks if a string might be in the bloom filter.
func (f *AtomicFilter) TestString(s string) bool {
	blockIdx, intraHash := hashString(s, f.numBlocks)
	return f.testWithHash(blockIdx, intraHash)
}

// testWithHash checks bits using pre-computed hash values.
func (f *AtomicFilter) testWithHash(blockIdx uint64, intraHash uint32) bool {
	blockBase := blockIdx * BlockWords

	for i := uint32(0); i < f.k; i++ {
		bitPos := f.offsets[i] + (intraHash % f.primes[i])
		wordIdx := bitPos / 64
		bitIdx := bitPos % 64
		if f.blocks[blockBase+uint64(wordIdx)].Load()&(1<<bitIdx) == 0 {
			return false
		}
	}

	return true
}

// TestAndAdd atomically tests if data is in the filter, then adds it.
// Note: This is NOT a single atomic operation - there's a race between
// test and add. Use this when you need best-effort deduplication.
func (f *AtomicFilter) TestAndAdd(data []byte) bool {
	blockIdx, intraHash := hashData(data, f.numBlocks)
	present := f.testWithHash(blockIdx, intraHash)
	f.addWithHash(blockIdx, intraHash)
	return present
}

// TestAndAddString atomically tests if a string is in the filter, then adds it.
func (f *AtomicFilter) TestAndAddString(s string) bool {
	blockIdx, intraHash := hashString(s, f.numBlocks)
	present := f.testWithHash(blockIdx, intraHash)
	f.addWithHash(blockIdx, intraHash)
	return present
}

// Cap returns the capacity of the filter in bits.
func (f *AtomicFilter) Cap() uint64 {
	return f.numBlocks * BlockBits
}

// K returns the number of hash functions (partitions) used.
func (f *AtomicFilter) K() uint32 {
	return f.k
}

// Count returns the approximate number of items added to the filter.
func (f *AtomicFilter) Count() uint64 {
	return f.count.Load()
}

// NumBlocks returns the number of 512-bit blocks in the filter.
func (f *AtomicFilter) NumBlocks() uint64 {
	return f.numBlocks
}

// EstimatedFillRatio estimates the proportion of bits that are set.
func (f *AtomicFilter) EstimatedFillRatio() float64 {
	var setBits uint64
	for i := range f.blocks {
		setBits += uint64(bits.OnesCount64(f.blocks[i].Load()))
	}
	return float64(setBits) / float64(f.numBlocks*BlockBits)
}

// EstimatedFalsePositiveRate estimates the current false positive rate.
func (f *AtomicFilter) EstimatedFalsePositiveRate() float64 {
	return EstimateFalsePositiveRate(f.numBlocks, f.k, f.count.Load())
}

// Clear resets the bloom filter to its initial empty state.
// This is NOT safe to call concurrently with other operations.
func (f *AtomicFilter) Clear() {
	for i := range f.blocks {
		f.blocks[i].Store(0)
	}
	f.count.Store(0)
}

// ShardedAtomicFilter is a thread-safe bloom filter that distributes writes
// across multiple shards to reduce contention under parallel workloads.
// Each shard is an independent AtomicFilter, and keys are consistently
// routed to shards based on their hash.
type ShardedAtomicFilter struct {
	shards    []*AtomicFilter
	numShards uint64
	mask      uint64 // numShards - 1, for fast modulo
}

// NewShardedAtomic creates a new sharded thread-safe bloom filter.
// numShards must be a power of 2 (will be rounded up if not).
// The total capacity is distributed evenly across shards.
func NewShardedAtomic(expectedItems uint64, fpRate float64, numShards uint64) *ShardedAtomicFilter {
	// Round up to power of 2 (nextPowerOf2 always returns >= 1)
	numShards = nextPowerOf2(numShards)

	// Distribute capacity across shards
	itemsPerShard := (expectedItems + numShards - 1) / numShards

	shards := make([]*AtomicFilter, numShards)
	for i := range shards {
		shards[i] = NewAtomic(itemsPerShard, fpRate)
	}

	return &ShardedAtomicFilter{
		shards:    shards,
		numShards: numShards,
		mask:      numShards - 1,
	}
}

// NewShardedAtomicDefault creates a sharded filter with a default number of
// shards based on typical concurrent workloads (16 shards).
func NewShardedAtomicDefault(expectedItems uint64, fpRate float64) *ShardedAtomicFilter {
	return NewShardedAtomic(expectedItems, fpRate, 16)
}

// Add adds data to the bloom filter.
func (f *ShardedAtomicFilter) Add(data []byte) {
	h := hashRaw(data)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitWithHash(h, shard.numBlocks)
	shard.addWithHash(blockIdx, intraHash)
}

// AddString adds a string to the bloom filter without allocating.
func (f *ShardedAtomicFilter) AddString(s string) {
	h := hashRawString(s)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitWithHash(h, shard.numBlocks)
	shard.addWithHash(blockIdx, intraHash)
}

// Test checks if data might be in the bloom filter.
func (f *ShardedAtomicFilter) Test(data []byte) bool {
	h := hashRaw(data)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitWithHash(h, shard.numBlocks)
	return shard.testWithHash(blockIdx, intraHash)
}

// TestString checks if a string might be in the bloom filter.
func (f *ShardedAtomicFilter) TestString(s string) bool {
	h := hashRawString(s)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitWithHash(h, shard.numBlocks)
	return shard.testWithHash(blockIdx, intraHash)
}

// TestAndAdd tests if data is in the filter, then adds it.
func (f *ShardedAtomicFilter) TestAndAdd(data []byte) bool {
	h := hashRaw(data)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitWithHash(h, shard.numBlocks)
	present := shard.testWithHash(blockIdx, intraHash)
	shard.addWithHash(blockIdx, intraHash)
	return present
}

// TestAndAddString tests if a string is in the filter, then adds it.
func (f *ShardedAtomicFilter) TestAndAddString(s string) bool {
	h := hashRawString(s)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitWithHash(h, shard.numBlocks)
	present := shard.testWithHash(blockIdx, intraHash)
	shard.addWithHash(blockIdx, intraHash)
	return present
}

// shardIndex extracts the shard index from a hash value.
// Uses bits 48-63 to avoid correlation with block selection (bits 32-47).
func (f *ShardedAtomicFilter) shardIndex(h uint64) uint64 {
	return (h >> 48) & f.mask
}

// Cap returns the total capacity of all shards in bits.
func (f *ShardedAtomicFilter) Cap() uint64 {
	var total uint64
	for _, shard := range f.shards {
		total += shard.Cap()
	}
	return total
}

// K returns the number of hash functions used per shard.
func (f *ShardedAtomicFilter) K() uint32 {
	return f.shards[0].K()
}

// Count returns the approximate total number of items added.
func (f *ShardedAtomicFilter) Count() uint64 {
	var total uint64
	for _, shard := range f.shards {
		total += shard.Count()
	}
	return total
}

// NumShards returns the number of shards.
func (f *ShardedAtomicFilter) NumShards() uint64 {
	return f.numShards
}

// NumBlocks returns the total number of blocks across all shards.
func (f *ShardedAtomicFilter) NumBlocks() uint64 {
	var total uint64
	for _, shard := range f.shards {
		total += shard.NumBlocks()
	}
	return total
}

// EstimatedFillRatio estimates the average fill ratio across all shards.
func (f *ShardedAtomicFilter) EstimatedFillRatio() float64 {
	var totalBits, setBits uint64
	for _, shard := range f.shards {
		totalBits += shard.Cap()
		setBits += uint64(float64(shard.Cap()) * shard.EstimatedFillRatio())
	}
	// totalBits is always > 0 since shards always have capacity
	return float64(setBits) / float64(totalBits)
}

// EstimatedFalsePositiveRate estimates the current false positive rate.
// For sharded filters, this is approximately the average across shards.
func (f *ShardedAtomicFilter) EstimatedFalsePositiveRate() float64 {
	var sum float64
	for _, shard := range f.shards {
		sum += shard.EstimatedFalsePositiveRate()
	}
	return sum / float64(f.numShards)
}

// Clear resets all shards to their initial empty state.
// This is NOT safe to call concurrently with other operations.
func (f *ShardedAtomicFilter) Clear() {
	for _, shard := range f.shards {
		shard.Clear()
	}
}

// nextPowerOf2 returns the smallest power of 2 >= n.
func nextPowerOf2(n uint64) uint64 {
	if n == 0 {
		return 1
	}
	n--
	n |= n >> 1
	n |= n >> 2
	n |= n >> 4
	n |= n >> 8
	n |= n >> 16
	n |= n >> 32
	return n + 1
}
