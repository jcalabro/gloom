# Gloom

[![Go Reference](https://pkg.go.dev/badge/github.com/jcalabro/gloom.svg)](https://pkg.go.dev/github.com/jcalabro/gloom)
[![CI](https://github.com/jcalabro/gloom/actions/workflows/ci.yaml/badge.svg)](https://github.com/jcalabro/gloom/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/jcalabro/gloom/branch/main/graph/badge.svg)](https://codecov.io/gh/jcalabro/gloom)
[![Go Report Card](https://goreportcard.com/badge/github.com/jcalabro/gloom)](https://goreportcard.com/report/github.com/jcalabro/gloom)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A high-performance bloom filter library for Go, implementing cache-line blocked one-hashing (OHBF).

## Features

- **Cache-line optimized**: All k bit probes for a key are aligned and fit within a single 64-byte cache line, minimizing memory access latency
- **One-hashing technique**: Uses a single xxh3 call with prime modulo partitions instead of k independent hash functions
- **Three implementations**:
  - `Filter` - Non-thread-safe, fastest for single-threaded workloads, allows for serialization/deserialization
  - `AtomicFilter` - Thread-safe using `atomic.Uint64.Or()`, best for read-heavy concurrent workloads
  - `ShardedAtomicFilter` - Thread-safe with sharding, best for write-heavy concurrent workloads
- **Zero allocations**: Hot paths (Add/Test) allocate no memory
- **100% test coverage**: Comprehensive test suite
- **Go 1.23+**: Uses modern atomic operations for best performance

## Usage

### Single-Threaded Usage

Requires the caller to synchronize parallel reads and writes, if any.

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
    defer wg.Wait()

    for i := range 8 {
        wg.Go(func() {
            for j := range 100000 {
                f.AddString(fmt.Sprintf("key-%d-%d", i, j))
            }
        })
    }
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
    // Create a sharded filter with auto-tuned shard count (based on GOMAXPROCS)
    f := gloom.NewShardedAtomicDefault(1_000_000, 0.01)

    // Or specify shard count explicitly (must be power of 2)
    // f := gloom.NewShardedAtomic(1_000_000, 0.01, 16)

    var wg sync.WaitGroup
    defer wg.Wait()

    for i := range 32 {
        wg.Go(func() {
            for j := range 100000 {
                f.AddString(fmt.Sprintf("key-%d-%d", i, j))
            }
        })
    }
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
2. **Uses one xxh3 call** per operation - upper 32 bits select the block, lower 32 bits are reused
3. **Partitions each block by k primes** - the same hash value mod different primes gives k independent bit positions

```
┌───────────────────────────────────┐
│          Key: "hello"             │
└─────────────────┬─────────────────┘
                  │
                  ▼
┌───────────────────────────────────┐
│         xxh3.Hash(key)            │
│    h = 0xA1B2C3D4_E5F67890        │
└─────────────────┬─────────────────┘
                  │
    ┌─────────────┴─────────────┐
    │                           │
    ▼                           ▼
┌─────────────────┐   ┌─────────────────┐
│  Block Index    │   │  Intra-Hash     │
│  h >> 32        │   │  uint32(h)      │
│  % numBlocks    │   │  = 0xE5F67890   │
│  = Block 2      │   │                 │
└────────┬────────┘   └────────┬────────┘
         │                     │
         └──────────┬──────────┘
                    ▼
┌───────────────────────────────────────────────────────────────────┐
│                      Bloom Filter Memory                          │
│ ┌─────────┬─────────┬─────────┬─────────┬─────────┬─────────────┐ │
│ │ Block 0 │ Block 1 │ Block 2 │ Block 3 │ Block 4 │ ...         │ │
│ │ 512 bits│ 512 bits│ 512 bits│ 512 bits│ 512 bits│             │ │
│ └─────────┴─────────┴────┬────┴─────────┴─────────┴─────────────┘ │
│                          │                                        │
│              ┌───────────┴───────────┐                            │
│              ▼   ONE cache line (64B)▼                            │
│ ┌────────────────────────────────────────────────────────────┐    │
│ │                    Block 2 (512 bits)                      │    │
│ │ ┌──────┬──────┬──────┬──────┬──────┬──────┬──────┐         │    │
│ │ │ 67   │ 71   │ 73   │ 79   │ 83   │ 89   │ 50   │ ← prime │    │
│ │ │ bits │ bits │ bits │ bits │ bits │ bits │ bits │   sizes │    │
│ │ └──┬───┴──┬───┴──┬───┴──┬───┴──┬───┴──┬───┴──┬───┘         │    │
│ │    │      │      │      │      │      │                    │    │
│ │    ▼      ▼      ▼      ▼      ▼      ▼      ▼             │    │
│ │  h%67   h%71   h%73   h%79   h%83   h%89   h%50            │    │
│ │  =23    =45    =12    =67    =34    =78    =40             │    │
│ │    │      │      │      │      │      │      │             │    │
│ │    ▼      ▼      ▼      ▼      ▼      ▼      ▼             │    │
│ │  SET    SET    SET    SET    SET    SET    SET             │    │
│ │ bit 23 bit 112 bit 185 bit 264 bit 347 bit 436 bit 486     │    │
│ └────────────────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────────────┘
```

### References

- [Space/Time Trade-offs in Hash Coding with Allowable Errors](https://dl.acm.org/doi/10.1145/362686.362692) - Original bloom filter paper (Bloom, 1970)
- [Network Applications of Bloom Filters: A Survey](https://www.eecs.harvard.edu/~michaelm/postscripts/im2005b.pdf) - Comprehensive survey (Broder & Mitzenmacher, 2004)
- [One-Hashing Bloom Filter](https://yangtonghome.github.io/uploads/One_Hashing.pdf) - Prime partition technique
- [Less Hashing, Same Performance](https://www.eecs.harvard.edu/~michaelm/postscripts/rsa2008.pdf) - Double hashing theory (Kirsch & Mitzenmacher, 2006)
- [RocksDB Bloom Filter](https://github.com/facebook/rocksdb/wiki/RocksDB-Bloom-Filter) - Cache-line blocking implementation
- [xxHash](https://github.com/Cyan4973/xxHash) - Extremely fast hash algorithm (used via [zeebo/xxh3](https://github.com/zeebo/xxh3))

## Benchmarks

The times shown below are the mean time per operation as reported by Go's `testing.B` framework. For latency percentiles, see the [Histogram Benchmarks](#histogram-benchmarks) section.

Benchmarks run on AMD Ryzen 9 9950X (32 threads), Go 1.23+, comparing against:
- [bits-and-blooms/bloom](https://github.com/bits-and-blooms/bloom) - Popular non-thread-safe implementation
- [jazware/atomic-bloom](https://github.com/jazware/atomic-bloom) - Thread-safe fork of bits-and-blooms using atomics
- [greatroar/blobloom](https://github.com/greatroar/blobloom) - Cache-blocked filter (requires pre-hashing)

### Sequential Performance (single-threaded)

| Operation | Gloom | Gloom Atomic | BitsAndBlooms | AtomicBloom | Blobloom* |
|-----------|-------|--------------|---------------|-------------|-----------|
| **Add** | 19.9 ns | 35.7 ns | 44.4 ns | 57.5 ns | 16.9 ns |
| **Test** | 16.3 ns | 17.4 ns | 40.7 ns | 41.4 ns | 5.5 ns |

*Blobloom requires pre-hashing input, so times exclude hash computation.

### Parallel Performance (GOMAXPROCS=32)

| Operation | Gloom Atomic | Gloom Sharded | AtomicBloom |
|-----------|--------------|---------------|-------------|
| **Parallel Add** | 51.3 ns | **11.2 ns** | 19.2 ns |
| **Parallel Test** | **0.96 ns** | 1.00 ns | 1.94 ns |
| **Mixed R/W** | 30.9 ns | **7.1 ns** | 19.6 ns |
| **High Contention** | 64.6 ns | **17.2 ns** | 43.1 ns |

### Throughput

| Implementation | Items/sec (8 goroutines) |
|----------------|--------------------------|
| Gloom (non-atomic) | 38.3M items/sec |
| Gloom Atomic | 19.4M items/sec |
| **Gloom Sharded** | **78.6M items/sec** |

### Histogram Benchmarks

Sample output (Add operations on the same hardware as above):

| Library | Mean | p50 | p99 | p9999 |
|---------|------|-----|-----|-------|
| Gloom | 57 ns | 30 ns | 60 ns | 401 ns |
| GloomAtomic | 76 ns | 50 ns | 100 ns | 501 ns |
| GloomSharded | 75 ns | 50 ns | 91 ns | 471 ns |
| BitsAndBlooms | 103 ns | 80 ns | 170 ns | 682 ns |
| AtomicBloom | 95 ns | 70 ns | 120 ns | 571 ns |
| Blobloom* | 52 ns | 30 ns | 60 ns | 380 ns |

### Running Tests and Benchmarks

```bash
# Using https://github.com/casey/just
just # runs the linter and short tests with the race detector enabled

just test
just t # runs without the race detector

just test-long
just t-long

just bench
just bench-long
```

### Tips

For maximum performance on modern x86-64 CPUs, ensure you're building with [GOAMD64=v2](https://go.dev/wiki/MinimumRequirements#microarchitecture-support) or above. This enables hardware POPCNT instructions (used for fill ratio estimation) without runtime CPU detection overhead. Ensure your CPU supports `popcnt` first.

## License

MIT
