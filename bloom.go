package gloom

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"runtime"
	"sync/atomic"
	"unsafe"
)

// cacheLineSize is the size of a CPU cache line in bytes.
const cacheLineSize = 64

// Filter is a non-thread-safe bloom filter using cache-line blocked
// one-hashing for optimal performance.
//
// The filter divides memory into 512-bit (64-byte) blocks that fit in a
// single CPU cache line. Each block is partitioned into k segments using
// distinct prime sizes, enabling the one-hashing technique where a single
// hash value generates k independent bit positions via modulo operations.
type Filter struct {
	raw       []byte   // Raw allocation to keep aligned memory alive for GC
	blocks    []uint64 // 8 uint64s per block = 512 bits (cache-line aligned)
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

	raw, blocks := makeAlignedUint64Slice(int(numBlocks * BlockWords))

	return &Filter{
		raw:       raw,
		blocks:    blocks,
		numBlocks: numBlocks,
		k:         k,
		primes:    primes,
		offsets:   ComputeOffsets(primes),
	}
}

// makeAlignedUint64Slice allocates a cache-line aligned slice of uint64.
// Returns the raw byte slice (to keep alive for GC) and the aligned uint64 slice.
func makeAlignedUint64Slice(n int) ([]byte, []uint64) {
	// Allocate with extra space for alignment
	raw := make([]byte, n*8+cacheLineSize-1)
	addr := uintptr(unsafe.Pointer(&raw[0]))
	offset := (cacheLineSize - int(addr%cacheLineSize)) % cacheLineSize
	aligned := unsafe.Slice((*uint64)(unsafe.Pointer(&raw[offset])), n)
	return raw, aligned
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

// Serialization constants and errors.
const (
	// serializeVersion is the current serialization format version.
	serializeVersion byte = 1

	// headerSize is the size of the serialization header in bytes.
	// Version (1) + K (4) + NumBlocks (8) + Count (8) = 21 bytes
	headerSize = 21
)

var (
	// ErrInvalidData is returned when the serialized data is invalid or corrupted.
	ErrInvalidData = errors.New("gloom: invalid serialized data")

	// ErrUnsupportedVersion is returned when the serialization version is not supported.
	ErrUnsupportedVersion = errors.New("gloom: unsupported serialization version")

	// ErrInvalidK is returned when k value in serialized data is not supported.
	ErrInvalidK = errors.New("gloom: invalid k value in serialized data")
)

// MarshalBinary serializes the bloom filter to a byte slice.
// The serialized format is:
//   - Version (1 byte): serialization format version
//   - K (4 bytes): number of hash functions (little-endian uint32)
//   - NumBlocks (8 bytes): number of 512-bit blocks (little-endian uint64)
//   - Count (8 bytes): number of items added (little-endian uint64)
//   - Blocks (numBlocks * 64 bytes): the bit array data (little-endian uint64s)
//
// The primes and offsets are not serialized as they can be derived from k.
func (f *Filter) MarshalBinary() ([]byte, error) {
	// Calculate total size: header + block data
	dataSize := f.numBlocks * BlockWords * 8
	totalSize := headerSize + dataSize

	buf := make([]byte, totalSize)

	// Write header
	buf[0] = serializeVersion
	binary.LittleEndian.PutUint32(buf[1:5], f.k)
	binary.LittleEndian.PutUint64(buf[5:13], f.numBlocks)
	binary.LittleEndian.PutUint64(buf[13:21], f.count)

	// Write block data
	offset := headerSize
	for _, word := range f.blocks {
		binary.LittleEndian.PutUint64(buf[offset:offset+8], word)
		offset += 8
	}

	return buf, nil
}

// UnmarshalBinary deserializes a bloom filter from a byte slice.
// Returns an error if the data is invalid or corrupted.
func UnmarshalBinary(data []byte) (*Filter, error) {
	if len(data) < headerSize {
		return nil, fmt.Errorf("%w: data too short (got %d bytes, need at least %d)", ErrInvalidData, len(data), headerSize)
	}

	// Read and validate version
	version := data[0]
	if version != serializeVersion {
		return nil, fmt.Errorf("%w: got version %d, expected %d", ErrUnsupportedVersion, version, serializeVersion)
	}

	// Read header fields
	k := binary.LittleEndian.Uint32(data[1:5])
	numBlocks := binary.LittleEndian.Uint64(data[5:13])
	count := binary.LittleEndian.Uint64(data[13:21])

	// Validate k
	primes := GetPrimePartition(k)
	if primes == nil {
		return nil, fmt.Errorf("%w: k=%d is not supported (valid range: 3-14)", ErrInvalidK, k)
	}

	// Validate numBlocks to prevent overflow in subsequent calculations.
	// Max safe value ensures numBlocks * BlockWords * 8 won't overflow uint64
	// and that we can safely convert to int for slice allocation.
	// We also require at least 1 block for a valid filter.
	const maxNumBlocks = uint64(1) << 50 // ~1 petabyte of data, more than enough
	if numBlocks == 0 {
		return nil, fmt.Errorf("%w: numBlocks cannot be zero", ErrInvalidData)
	}
	if numBlocks > maxNumBlocks {
		return nil, fmt.Errorf("%w: numBlocks too large (%d)", ErrInvalidData, numBlocks)
	}

	// Validate data length (safe from overflow now that numBlocks is bounded)
	expectedDataLen := numBlocks * BlockWords * 8
	expectedTotalLen := headerSize + expectedDataLen
	if uint64(len(data)) != expectedTotalLen {
		return nil, fmt.Errorf("%w: data length mismatch (got %d bytes, expected %d)", ErrInvalidData, len(data), expectedTotalLen)
	}

	// Allocate aligned memory for blocks
	raw, blocks := makeAlignedUint64Slice(int(numBlocks * BlockWords))

	// Read block data
	offset := headerSize
	for i := range blocks {
		blocks[i] = binary.LittleEndian.Uint64(data[offset : offset+8])
		offset += 8
	}

	return &Filter{
		raw:       raw,
		blocks:    blocks,
		numBlocks: numBlocks,
		k:         k,
		primes:    primes,
		offsets:   ComputeOffsets(primes),
		count:     count,
	}, nil
}

// AtomicFilter is a thread-safe bloom filter using atomic operations.
// It uses the same cache-line blocked one-hashing technique as Filter
// but with atomic.Uint64 for concurrent access.
type AtomicFilter struct {
	raw       []byte          // Raw allocation to keep aligned memory alive for GC
	blocks    []atomic.Uint64 // 8 atomic uint64s per block = 512 bits (cache-line aligned)
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

	raw, blocks := makeAlignedAtomicUint64Slice(int(numBlocks * BlockWords))

	return &AtomicFilter{
		raw:       raw,
		blocks:    blocks,
		numBlocks: numBlocks,
		k:         k,
		primes:    primes,
		offsets:   ComputeOffsets(primes),
	}
}

// makeAlignedAtomicUint64Slice allocates a cache-line aligned slice of atomic.Uint64.
// Returns the raw byte slice (to keep alive for GC) and the aligned atomic slice.
func makeAlignedAtomicUint64Slice(n int) ([]byte, []atomic.Uint64) {
	// atomic.Uint64 is the same size as uint64 (8 bytes)
	const atomicSize = 8
	// Allocate with extra space for alignment
	raw := make([]byte, n*atomicSize+cacheLineSize-1)
	addr := uintptr(unsafe.Pointer(&raw[0]))
	offset := (cacheLineSize - int(addr%cacheLineSize)) % cacheLineSize
	aligned := unsafe.Slice((*atomic.Uint64)(unsafe.Pointer(&raw[offset])), n)
	return raw, aligned
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

// NewShardedAtomicDefault creates a sharded filter with a number of shards
// automatically tuned to the current GOMAXPROCS value. This provides good
// parallel performance without over-sharding on smaller machines.
func NewShardedAtomicDefault(expectedItems uint64, fpRate float64) *ShardedAtomicFilter {
	numShards := max(uint64(runtime.GOMAXPROCS(0)), 4)
	return NewShardedAtomic(expectedItems, fpRate, numShards)
}

// Add adds data to the bloom filter.
func (f *ShardedAtomicFilter) Add(data []byte) {
	h := hashRaw(data)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitSharded(h, shard.numBlocks)
	shard.addWithHash(blockIdx, intraHash)
}

// AddString adds a string to the bloom filter without allocating.
func (f *ShardedAtomicFilter) AddString(s string) {
	h := hashRawString(s)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitSharded(h, shard.numBlocks)
	shard.addWithHash(blockIdx, intraHash)
}

// Test checks if data might be in the bloom filter.
func (f *ShardedAtomicFilter) Test(data []byte) bool {
	h := hashRaw(data)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitSharded(h, shard.numBlocks)
	return shard.testWithHash(blockIdx, intraHash)
}

// TestString checks if a string might be in the bloom filter.
func (f *ShardedAtomicFilter) TestString(s string) bool {
	h := hashRawString(s)
	shard := f.shards[f.shardIndex(h)]
	blockIdx, intraHash := hashSplitSharded(h, shard.numBlocks)
	return shard.testWithHash(blockIdx, intraHash)
}

// shardIndex extracts the shard index from a hash value.
// Uses bits 32-47, which are non-overlapping with block selection (bits 48-63)
// and intra-block hashing (bits 0-31).
func (f *ShardedAtomicFilter) shardIndex(h uint64) uint64 {
	return (h >> 32) & f.mask
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
