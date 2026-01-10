// Package gloom provides high-performance bloom filter implementations for Go.
//
// A bloom filter is a space-efficient probabilistic data structure that tests
// whether an element is a member of a set. False positive matches are possible,
// but false negatives are not – if the filter says an element is not present,
// it definitely is not. If it says an element might be present, it could be a
// false positive.
//
// # Architecture
//
// Gloom uses two key optimizations for maximum throughput:
//
// Cache-line blocking: Each bloom filter is divided into 512-bit (64-byte) blocks
// that match the CPU cache line size. All k hash probes for a single key access
// the same block, meaning only one cache line is touched per operation. This
// dramatically reduces memory latency compared to traditional bloom filters
// where k probes might touch k different cache lines.
//
// One-hashing with prime partitions: Instead of computing k independent hash
// functions, gloom computes a single xxh3 hash and derives k bit positions
// using modulo operations with distinct prime numbers. This technique, based
// on the paper "One-Hashing Bloom Filter", provides excellent bit distribution
// while being much faster than multiple hash computations.
//
// # Implementations
//
// Three filter implementations are provided for different use cases:
//
// [Filter] is the fastest option for single-threaded workloads. It has no
// synchronization overhead and achieves ~20ns per Add/Test operation.
//
// [AtomicFilter] provides thread-safety using lock-free atomic operations.
// Multiple goroutines can safely call Add and Test concurrently. It uses
// [sync/atomic.Uint64.Or] (Go 1.23+) for efficient atomic bit-setting.
//
// [ShardedAtomicFilter] distributes keys across multiple independent shards
// to reduce contention under heavy parallel writes. The shard count is
// auto-tuned to GOMAXPROCS by default. Use this when you have many goroutines
// performing concurrent writes.
//
// # Choosing Parameters
//
// Use [New], [NewAtomic], or [NewShardedAtomicDefault] with your expected
// number of items and desired false positive rate:
//
//	// Filter for 1 million items with 1% false positive rate
//	f := gloom.New(1_000_000, 0.01)
//
// The functions automatically calculate optimal filter size and number of
// hash functions. For advanced use cases, [NewWithParams] and [NewAtomicWithParams]
// allow explicit control over the number of 512-bit blocks and hash functions.
//
// # False Positive Rate
//
// The false positive rate depends on:
//   - Filter capacity (number of blocks)
//   - Number of hash functions (k)
//   - Number of items added
//
// When the filter is filled to its intended capacity, it will achieve
// approximately the target false positive rate. Adding more items than
// the capacity increases the false positive rate. Use [Filter.EstimatedFalsePositiveRate]
// to monitor the current rate.
//
// # Memory Usage
//
// Memory usage is determined by the number of 512-bit blocks:
//
//	memory_bytes = num_blocks * 64
//
// For a filter sized for n items with false positive rate p:
//
//	memory_bits ≈ -n * ln(p) / (ln(2))²
//
// Example: 1 million items at 1% FP rate ≈ 1.2 MB
//
// # Thread Safety
//
// [Filter] is NOT thread-safe. Use external synchronization or choose
// [AtomicFilter] or [ShardedAtomicFilter] for concurrent access.
//
// [AtomicFilter] and [ShardedAtomicFilter] are safe for concurrent Add and
// Test operations.
//
// The [AtomicFilter.TestAndAdd] method is NOT a single atomic operation –
// there is a race window between the test and add. Use it for best-effort
// deduplication, not strict mutual exclusion.
//
// # Performance Tips
//
//   - Use [Filter] for single-threaded workloads (fastest)
//   - Use [ShardedAtomicFilter] for write-heavy concurrent workloads
//   - Use [AtomicFilter] for read-heavy concurrent workloads
//   - Use string methods ([Filter.AddString], [Filter.TestString]) to avoid
//     allocating when you have string keys
//   - Build with GOAMD64=v2 or higher to enable hardware POPCNT for
//     [Filter.EstimatedFillRatio]
//
// # References
//
//   - One-Hashing Bloom Filter: https://yangtonghome.github.io/uploads/One_Hashing.pdf
//   - Cache-line blocking (RocksDB): https://github.com/facebook/rocksdb/wiki/RocksDB-Bloom-Filter
//   - Less Hashing, Same Performance: https://www.eecs.harvard.edu/~michaelm/postscripts/rsa2008.pdf
package gloom
