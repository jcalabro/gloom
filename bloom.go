package gloom

import (
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
		setBits += uint64(popcount(word))
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

// popcount returns the number of set bits in x.
func popcount(x uint64) int {
	// Using the standard popcount algorithm
	const m1 = 0x5555555555555555
	const m2 = 0x3333333333333333
	const m4 = 0x0f0f0f0f0f0f0f0f
	const h01 = 0x0101010101010101

	x -= (x >> 1) & m1
	x = (x & m2) + ((x >> 2) & m2)
	x = (x + (x >> 4)) & m4
	return int((x * h01) >> 56)
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
		setBits += uint64(popcount(f.blocks[i].Load()))
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
