package benchmarks

import (
	"fmt"
	"sync"
	"testing"

	bab "github.com/bits-and-blooms/bloom/v3"
	"github.com/cespare/xxhash/v2"
	atomicbloom "github.com/ericvolp12/atomic-bloom"
	"github.com/greatroar/blobloom"
	"github.com/jcalabro/gloom"
)

const (
	benchItems  = 1_000_000
	benchFPRate = 0.01
)

// Pre-generate test data to avoid measuring string generation
var testKeys [][]byte
var testKeysStr []string

func init() {
	testKeys = make([][]byte, benchItems)
	testKeysStr = make([]string, benchItems)
	for i := range benchItems {
		s := fmt.Sprintf("key-%d", i)
		testKeys[i] = []byte(s)
		testKeysStr[i] = s
	}
}

// ============================================================================
// Sequential Add Benchmarks
// ============================================================================

func BenchmarkAddSequential_Gloom(b *testing.B) {
	f := gloom.New(benchItems, benchFPRate)
	b.ResetTimer()
	for i := range b.N {
		f.Add(testKeys[i%benchItems])
	}
}

func BenchmarkAddSequential_GloomString(b *testing.B) {
	f := gloom.New(benchItems, benchFPRate)
	b.ResetTimer()
	for i := range b.N {
		f.AddString(testKeysStr[i%benchItems])
	}
}

func BenchmarkAddSequential_GloomAtomic(b *testing.B) {
	f := gloom.NewAtomic(benchItems, benchFPRate)
	b.ResetTimer()
	for i := range b.N {
		f.Add(testKeys[i%benchItems])
	}
}

func BenchmarkAddSequential_BitsAndBlooms(b *testing.B) {
	f := bab.NewWithEstimates(benchItems, benchFPRate)
	b.ResetTimer()
	for i := range b.N {
		f.Add(testKeys[i%benchItems])
	}
}

func BenchmarkAddSequential_AtomicBloom(b *testing.B) {
	f := atomicbloom.NewWithEstimates(benchItems, benchFPRate)
	b.ResetTimer()
	for i := range b.N {
		f.Add(testKeys[i%benchItems])
	}
}

func BenchmarkAddSequential_Blobloom(b *testing.B) {
	f := blobloom.NewOptimized(blobloom.Config{
		Capacity: benchItems,
		FPRate:   benchFPRate,
	})
	b.ResetTimer()
	for i := range b.N {
		// blobloom requires pre-hashing
		h := xxhash.Sum64(testKeys[i%benchItems])
		f.Add(h)
	}
}

// ============================================================================
// Sequential Test Benchmarks
// ============================================================================

func BenchmarkTestSequential_Gloom(b *testing.B) {
	f := gloom.New(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	for i := range b.N {
		f.Test(testKeys[i%benchItems])
	}
}

func BenchmarkTestSequential_GloomString(b *testing.B) {
	f := gloom.New(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	for i := range b.N {
		f.TestString(testKeysStr[i%benchItems])
	}
}

func BenchmarkTestSequential_GloomAtomic(b *testing.B) {
	f := gloom.NewAtomic(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	for i := range b.N {
		f.Test(testKeys[i%benchItems])
	}
}

func BenchmarkTestSequential_BitsAndBlooms(b *testing.B) {
	f := bab.NewWithEstimates(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	for i := range b.N {
		f.Test(testKeys[i%benchItems])
	}
}

func BenchmarkTestSequential_AtomicBloom(b *testing.B) {
	f := atomicbloom.NewWithEstimates(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	for i := range b.N {
		f.Test(testKeys[i%benchItems])
	}
}

func BenchmarkTestSequential_Blobloom(b *testing.B) {
	f := blobloom.NewOptimized(blobloom.Config{
		Capacity: benchItems,
		FPRate:   benchFPRate,
	})
	// Pre-hash keys for fair comparison
	hashes := make([]uint64, benchItems)
	for i := range benchItems {
		hashes[i] = xxhash.Sum64(testKeys[i])
		f.Add(hashes[i])
	}
	b.ResetTimer()
	for i := range b.N {
		f.Has(hashes[i%benchItems])
	}
}

// ============================================================================
// Parallel Add Benchmarks (8 goroutines)
// ============================================================================

func BenchmarkAddParallel_GloomAtomic(b *testing.B) {
	f := gloom.NewAtomic(benchItems, benchFPRate)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			f.Add(testKeys[i%benchItems])
			i++
		}
	})
}

func BenchmarkAddParallel_AtomicBloom(b *testing.B) {
	f := atomicbloom.NewWithEstimates(benchItems, benchFPRate)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			f.Add(testKeys[i%benchItems])
			i++
		}
	})
}

// ============================================================================
// Parallel Test Benchmarks (8 goroutines)
// ============================================================================

func BenchmarkTestParallel_GloomAtomic(b *testing.B) {
	f := gloom.NewAtomic(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			f.Test(testKeys[i%benchItems])
			i++
		}
	})
}

func BenchmarkTestParallel_AtomicBloom(b *testing.B) {
	f := atomicbloom.NewWithEstimates(benchItems, benchFPRate)
	for i := range benchItems {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			f.Test(testKeys[i%benchItems])
			i++
		}
	})
}

// ============================================================================
// Mixed Read/Write Benchmarks (50/50 split)
// ============================================================================

func BenchmarkMixed_GloomAtomic(b *testing.B) {
	f := gloom.NewAtomic(benchItems, benchFPRate)
	// Pre-populate half
	for i := 0; i < benchItems/2; i++ {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				f.Add(testKeys[(benchItems/2+i)%benchItems])
			} else {
				f.Test(testKeys[i%benchItems])
			}
			i++
		}
	})
}

func BenchmarkMixed_AtomicBloom(b *testing.B) {
	f := atomicbloom.NewWithEstimates(benchItems, benchFPRate)
	// Pre-populate half
	for i := 0; i < benchItems/2; i++ {
		f.Add(testKeys[i])
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				f.Add(testKeys[(benchItems/2+i)%benchItems])
			} else {
				f.Test(testKeys[i%benchItems])
			}
			i++
		}
	})
}

// ============================================================================
// Memory Allocation Benchmarks
// ============================================================================

func BenchmarkAddAlloc_Gloom(b *testing.B) {
	f := gloom.New(benchItems, benchFPRate)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		f.Add(testKeys[i%benchItems])
	}
}

func BenchmarkAddAlloc_GloomString(b *testing.B) {
	f := gloom.New(benchItems, benchFPRate)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		f.AddString(testKeysStr[i%benchItems])
	}
}

func BenchmarkAddAlloc_BitsAndBlooms(b *testing.B) {
	f := bab.NewWithEstimates(benchItems, benchFPRate)
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		f.Add(testKeys[i%benchItems])
	}
}

// ============================================================================
// High Contention Benchmarks
// ============================================================================

func BenchmarkHighContention_GloomAtomic(b *testing.B) {
	// Use a small filter to maximize contention
	f := gloom.NewAtomic(1000, benchFPRate)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			// All goroutines write to same small set of blocks
			f.Add(testKeys[i%1000])
			i++
		}
	})
}

func BenchmarkHighContention_AtomicBloom(b *testing.B) {
	f := atomicbloom.NewWithEstimates(1000, benchFPRate)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			f.Add(testKeys[i%1000])
			i++
		}
	})
}

// ============================================================================
// Throughput Test (items per second)
// ============================================================================

func BenchmarkThroughput_Gloom(b *testing.B) {
	const goroutines = 8
	const itemsPerGoroutine = 100000

	f := gloom.New(uint64(goroutines*itemsPerGoroutine), benchFPRate)

	b.ResetTimer()
	for range b.N {
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := range goroutines {
			go func(gid int) {
				defer wg.Done()
				base := gid * itemsPerGoroutine
				for i := range itemsPerGoroutine {
					f.Add(testKeys[(base+i)%benchItems])
				}
			}(g)
		}
		wg.Wait()
	}
	b.ReportMetric(float64(goroutines*itemsPerGoroutine), "items/op")
}

func BenchmarkThroughput_GloomAtomic(b *testing.B) {
	const goroutines = 8
	const itemsPerGoroutine = 100000

	f := gloom.NewAtomic(uint64(goroutines*itemsPerGoroutine), benchFPRate)

	b.ResetTimer()
	for range b.N {
		var wg sync.WaitGroup
		wg.Add(goroutines)
		for g := range goroutines {
			go func(gid int) {
				defer wg.Done()
				base := gid * itemsPerGoroutine
				for i := range itemsPerGoroutine {
					f.Add(testKeys[(base+i)%benchItems])
				}
			}(g)
		}
		wg.Wait()
	}
	b.ReportMetric(float64(goroutines*itemsPerGoroutine), "items/op")
}
