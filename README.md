# Gloom

[![Go Reference](https://pkg.go.dev/badge/github.com/jcalabro/gloom.svg)](https://pkg.go.dev/github.com/jcalabro/gloom)
[![CI](https://github.com/jcalabro/gloom/actions/workflows/ci.yaml/badge.svg)](https://github.com/jcalabro/gloom/actions/workflows/ci.yaml)
[![codecov](https://codecov.io/gh/jcalabro/gloom/branch/main/graph/badge.svg)](https://codecov.io/gh/jcalabro/gloom)
[![Go Report Card](https://goreportcard.com/badge/github.com/jcalabro/gloom)](https://goreportcard.com/report/github.com/jcalabro/gloom)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

A high-performance bloom filter library for Go, implementing cache-line blocked one-hashing (OHBF).

## Features

- Cache-line optimized: All k bit probes for a key are aligned and fit within a single 64-byte cache line, minimizing memory access latency
- One-hashing technique: Uses a single xxh3 (128-bit) call with prime modulo partitions instead of k independent hash functions
- Three implementations:
  - `Filter` - Non-thread-safe, fastest for single-threaded workloads, supports serialization/deserialization (with a CRC integrity check)
  - `AtomicFilter` - Thread-safe using `atomic.Uint64.Or()`, with a striped counter so concurrent writes scale across cores
  - `ShardedAtomicFilter` - Thread-safe with sharding, best for the highest write concurrency
- Scales to billions of items: block and shard selection use the full hash width (no capacity cliff), and `OptimalParams` compensates for the cache-line blocking penalty so the realized false-positive rate meets the target
- Zero allocations: Hot paths (Add/Test) allocate no memory
- 100% test coverage: Comprehensive test suite

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

`AtomicFilter` now scales well under concurrent writes on its own (its item counter is striped
across cache lines), so for most concurrent workloads it is the simplest choice.
`ShardedAtomicFilter` squeezes out the last bit of write throughput on machines with very many
cores by splitting the filter into independent shards; the capacity you pass is divided evenly
across them:

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
2. **Uses one xxh3 (128-bit) call** per operation — the high 64 bits select the block (via a multiply-shift range reduction, so there is no capacity cliff and no modulo bias at any block count), and the low 64 bits are folded into the intra-block hash
3. **Partitions each block by k pairwise-coprime sizes** — the same hash value mod different sizes gives k independent bit positions (a Chinese Remainder Theorem argument; this is why the sizes must be coprime, not merely distinct)

```
┌───────────────────────────────────┐
│          Key: "hello"             │
└─────────────────┬─────────────────┘
                  │
                  ▼
┌───────────────────────────────────┐
│       xxh3.Hash128(key)           │
│  hi = 0xA1B2C3D4...  lo = 0x...    │
└─────────────────┬─────────────────┘
                  │
    ┌─────────────┴─────────────┐
    │                           │
    ▼                           ▼
┌─────────────────┐   ┌─────────────────┐
│  Block Index    │   │  Intra-Hash     │
│  reduceRange(   │   │  fold(lo) ->    │
│   hi,numBlocks) │   │  uint32         │
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

### Running Tests and Benchmarks

```bash
# Using https://github.com/casey/just
just           # runs the linter and short tests

just test      # short tests, no race detector
just test-race # short tests, with race detector
just test-long # all tests including long-running ones

just bench
just bench-long
```

### Tips

For maximum performance on modern x86-64 CPUs, build with [GOAMD64=v2](https://go.dev/wiki/MinimumRequirements#microarchitecture-support) or above. This enables hardware POPCNT (used by `EstimatedFillRatio`/`SampledFillRatio`) without runtime CPU detection overhead. Ensure your CPU supports `popcnt` first. The benchmark numbers above were collected at `v2`; the default `v1` build is slightly slower on the fill-ratio paths.

## License

MIT
