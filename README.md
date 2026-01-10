# Gloom

A high-performance bloom filter library for Go, implementing cache-line blocked one-hashing for optimal throughput.

## Features

- **Cache-line optimized**: All k bit probes for a key fit within a single 64-byte cache line, minimizing memory access latency
- **One-hashing technique**: Uses a single xxhash64 call with prime modulo partitions instead of k independent hash functions
- **Two implementations**: Non-thread-safe `Filter` and thread-safe `AtomicFilter` using `atomic.Uint64.Or()`
- **Zero allocations**: Hot paths (Add/Test) allocate no memory
- **Go 1.23+**: Uses modern atomic operations for best performance

## Installation

```bash
go get github.com/jcalabro/gloom
```

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
    for i := 0; i < 8; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            for j := 0; j < 100000; j++ {
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
- [ericvolp12/atomic-bloom](https://github.com/ericvolp12/atomic-bloom) - Thread-safe fork using atomics
- [greatroar/blobloom](https://github.com/greatroar/blobloom) - Cache-blocked filter (requires pre-hashing)

### Sequential Performance (single-threaded)

| Operation | Gloom | Gloom Atomic | BitsAndBlooms | AtomicBloom | Blobloom* |
|-----------|-------|--------------|---------------|-------------|-----------|
| **Add** | 23.3 ns | 35.9 ns | 44.9 ns | 58.2 ns | 17.0 ns |
| **Test** | 19.5 ns | 20.8 ns | 41.6 ns | 41.7 ns | 5.4 ns |

*Blobloom requires pre-hashing input, so times exclude hash computation.

### Parallel Performance (GOMAXPROCS=32)

| Operation | Gloom Atomic | AtomicBloom | Winner |
|-----------|--------------|-------------|--------|
| **Parallel Add** | 53.1 ns | 18.6 ns | AtomicBloom |
| **Parallel Test** | **1.06 ns** | 3.2 ns | **Gloom 3x faster** |
| **Mixed R/W** | 29.9 ns | 12.2 ns | AtomicBloom |
| **High Contention** | 66.1 ns | 37.6 ns | AtomicBloom |

### Throughput

| Implementation | Items/sec (8 goroutines) |
|----------------|--------------------------|
| Gloom (non-atomic) | **34.4M items/sec** |
| Gloom Atomic | 18.7M items/sec |

### Analysis

**Strengths:**
- Sequential operations are 1.9-2.5x faster than competitors
- Parallel reads are extremely fast (~1 ns) due to cache-line locality
- Zero allocations in all operations

**Trade-off - Write Contention:**
The cache-line blocking approach creates write contention under parallel workloads. When multiple goroutines write concurrently, they may contend on the same 512-bit block. AtomicBloom spreads writes across the entire filter, reducing contention at the cost of cache efficiency.

**Recommendation:**
- Use `Filter` for single-threaded workloads (fastest)
- Use `AtomicFilter` for read-heavy concurrent workloads
- Consider AtomicBloom for write-heavy concurrent workloads with high parallelism

### Running Benchmarks

```bash
# Using just
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

## License

MIT
