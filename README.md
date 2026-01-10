# Gloom

A high-performance bloom filter library for Go, implementing cache-line blocked one-hashing for optimal throughput.

## Features

- **Cache-line optimized**: All k bit probes for a key fit within a single 64-byte cache line, minimizing memory access latency
- **One-hashing technique**: Uses a single xxhash64 call with prime modulo partitions instead of k independent hash functions
- **Three implementations**:
  - `Filter` - Non-thread-safe, fastest for single-threaded workloads
  - `AtomicFilter` - Thread-safe using `atomic.Uint64.Or()`, best for read-heavy concurrent workloads
  - `ShardedAtomicFilter` - Thread-safe with sharding, best for write-heavy concurrent workloads
- **Zero allocations**: Hot paths (Add/Test) allocate no memory
- **100% test coverage**: Comprehensive test suite
- **Go 1.23+**: Uses modern atomic operations for best performance

## Usage

### Basic Usage

```go
package main

import "github.com/jcalabro/gloom"

func main() {
    // Create a filter for 1 million items with 1% false positive rate
    f := gloom.New(1_000_000, 0.01)

    // Add items
    f.Add([]byte("hello"))
    f.AddString("world")

    // Test membership
    if f.Test([]byte("hello")) {
        println("hello might be present")
    }
    if !f.TestString("not-added") {
        println("definitely not present")
    }
}
```

### Thread-Safe Usage

```go
package main

import (
    "sync"
    "github.com/jcalabro/gloom"
)

func main() {
    // Create an atomic filter for concurrent access
    f := gloom.NewAtomic(1_000_000, 0.01)

    var wg sync.WaitGroup
    for i := range 8 {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := range 100000 {
                f.AddString(fmt.Sprintf("key-%d-%d", id, j))
            }
        }(i)
    }
    wg.Wait()
}
```

### High-Throughput Concurrent Writes

For write-heavy concurrent workloads, use `ShardedAtomicFilter` which distributes writes across multiple independent shards to reduce contention:

```go
package main

import (
    "sync"
    "github.com/jcalabro/gloom"
)

func main() {
    // Create a sharded filter with 16 shards for high write throughput
    f := gloom.NewShardedAtomic(1_000_000, 0.01, 16)

    // Or use the default (16 shards)
    // f := gloom.NewShardedAtomicDefault(1_000_000, 0.01)

    var wg sync.WaitGroup
    for i := range 32 {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := range 100000 {
                f.AddString(fmt.Sprintf("key-%d-%d", id, j))
            }
        }(i)
    }
    wg.Wait()
}
```

### Advanced Configuration

```go
// Create with explicit parameters
// numBlocks: number of 512-bit cache-line blocks
// k: number of hash functions (partitions)
f := gloom.NewWithParams(1000, 7)

// Get filter statistics
fmt.Printf("Capacity: %d bits\n", f.Cap())
fmt.Printf("Hash functions: %d\n", f.K())
fmt.Printf("Items added: %d\n", f.Count())
fmt.Printf("Fill ratio: %.2f%%\n", f.EstimatedFillRatio()*100)
fmt.Printf("Est. FP rate: %.4f%%\n", f.EstimatedFalsePositiveRate()*100)
```

## Design

### Cache-Line Blocked One-Hashing

Traditional bloom filters use k independent hash functions, each potentially accessing a different cache line. Gloom instead:

1. **Blocks memory into 512-bit (64-byte) chunks** matching CPU cache line size
2. **Uses one xxhash64 call** per operation - upper 32 bits select the block, lower 32 bits are reused
3. **Partitions each block by k primes** - the same hash value mod different primes gives k independent bit positions

```
┌─────────────────────────────────────────────────────────────┐
│                    Bloom Filter Memory                       │
├──────────┬──────────┬──────────┬──────────┬────────────────┤
│ Block 0  │ Block 1  │ Block 2  │ Block 3  │ ...            │
│ 512 bits │ 512 bits │ 512 bits │ 512 bits │                │
└──────────┴──────────┴──────────┴──────────┴────────────────┘

Within each block (k=7 example):
┌─────┬─────┬─────┬─────┬─────┬─────┬─────┐
│ p=67│ p=71│ p=73│ p=79│ p=83│ p=89│ p=50│  (sum=512)
└─────┴─────┴─────┴─────┴─────┴─────┴─────┘
```

### References

- [One-Hashing Bloom Filter](https://yangtonghome.github.io/uploads/One_Hashing.pdf) - Prime partition technique
- [RocksDB Bloom Filter](https://github.com/facebook/rocksdb/wiki/RocksDB-Bloom-Filter) - Cache-line blocking
- [Less Hashing, Same Performance](https://www.eecs.harvard.edu/~michaelm/postscripts/rsa2008.pdf) - Double hashing theory

## Benchmarks

Benchmarks run on AMD Ryzen 9 9950X (32 threads), Go 1.23+, comparing against:
- [bits-and-blooms/bloom](https://github.com/bits-and-blooms/bloom) - Popular non-thread-safe implementation
- [jazware/atomic-bloom](https://github.com/jazware/atomic-bloom) - Thread-safe fork using atomics
- [greatroar/blobloom](https://github.com/greatroar/blobloom) - Cache-blocked filter (requires pre-hashing)

### Sequential Performance (single-threaded)

| Operation | Gloom | Gloom Atomic | BitsAndBlooms | AtomicBloom | Blobloom* |
|-----------|-------|--------------|---------------|-------------|-----------|
| **Add** | 23.3 ns | 35.9 ns | 44.9 ns | 58.2 ns | 17.0 ns |
| **Test** | 19.5 ns | 20.8 ns | 41.6 ns | 41.7 ns | 5.4 ns |

*Blobloom requires pre-hashing input, so times exclude hash computation.

### Parallel Performance (GOMAXPROCS=32)

| Operation | Gloom Atomic | Gloom Sharded | AtomicBloom |
|-----------|--------------|---------------|-------------|
| **Parallel Add** | 48.8 ns | **13.3 ns** | 19.3 ns |
| **Parallel Test** | **1.06 ns** | 1.12 ns | 1.95 ns |
| **Mixed R/W** | 29.9 ns | **8.3 ns** | 12.2 ns |
| **High Contention** | 65.9 ns | **20.6 ns** | 29.6 ns |

The sharded filter achieves **3.7x faster parallel writes** than AtomicFilter by distributing operations across 16 independent shards.

### Throughput

| Implementation | Items/sec (8 goroutines) |
|----------------|--------------------------|
| Gloom (non-atomic) | 37.0M items/sec |
| Gloom Atomic | 18.5M items/sec |
| **Gloom Sharded** | **74.5M items/sec** |

### Running Benchmarks

```bash
# Using just
just bench
just bench-long

# Or manually
cd benchmarks && go test -bench=. -benchmem -benchtime=3s ./...
```

## API Reference

### Filter (Non-Thread-Safe)

```go
func New(expectedItems uint64, fpRate float64) *Filter
func NewWithParams(numBlocks uint64, k uint32) *Filter

func (f *Filter) Add(data []byte)
func (f *Filter) AddString(s string)
func (f *Filter) Test(data []byte) bool
func (f *Filter) TestString(s string) bool
func (f *Filter) TestAndAdd(data []byte) bool
func (f *Filter) TestAndAddString(s string) bool
func (f *Filter) Clear()

func (f *Filter) Cap() uint64                      // Capacity in bits
func (f *Filter) K() uint32                        // Number of hash functions
func (f *Filter) Count() uint64                    // Items added
func (f *Filter) NumBlocks() uint64                // Number of 512-bit blocks
func (f *Filter) EstimatedFillRatio() float64      // Proportion of bits set
func (f *Filter) EstimatedFalsePositiveRate() float64
```

### AtomicFilter (Thread-Safe)

Same API as `Filter`, safe for concurrent use. Uses `atomic.Uint64.Or()` for lock-free bit setting.

### ShardedAtomicFilter (Thread-Safe, High Write Throughput)

Same API as `Filter`, safe for concurrent use. Distributes keys across N independent `AtomicFilter` shards to reduce write contention. Use when you have highly parallel write throughput requirements.

Also has this additional method:

```go
func (f *ShardedAtomicFilter) NumBlocks() uint64
```

## License

MIT
