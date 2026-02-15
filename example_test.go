package gloom_test

import (
	"fmt"
	"sync"

	"github.com/jcalabro/gloom"
)

// This example demonstrates basic bloom filter usage for membership testing.
func Example() {
	// Create a filter for 10,000 items with 1% false positive rate
	f := gloom.New(10_000, 0.01)

	// Add some items
	f.Add([]byte("apple"))
	f.Add([]byte("banana"))
	f.Add([]byte("cherry"))

	// Test membership
	fmt.Println("apple:", f.Test([]byte("apple")))   // true (added)
	fmt.Println("banana:", f.Test([]byte("banana"))) // true (added)
	fmt.Println("grape:", f.Test([]byte("grape")))   // false (not added)

	// Output:
	// apple: true
	// banana: true
	// grape: false
}

// This example shows how to use string keys without allocation overhead.
func Example_stringKeys() {
	f := gloom.New(10_000, 0.01)

	// AddString and TestString avoid allocating when you have string keys
	f.AddString("user:12345")
	f.AddString("user:67890")

	fmt.Println("user:12345 exists:", f.TestString("user:12345"))
	fmt.Println("user:99999 exists:", f.TestString("user:99999"))

	// Output:
	// user:12345 exists: true
	// user:99999 exists: false
}

// This example demonstrates using AtomicFilter for concurrent access.
func Example_concurrent() {
	// AtomicFilter is safe for concurrent Add and Test
	f := gloom.NewAtomic(100_000, 0.01)

	var wg sync.WaitGroup

	// Spawn multiple writers
	for i := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 1000 {
				key := fmt.Sprintf("worker-%d-item-%d", id, j)
				f.AddString(key)
			}
		}(i)
	}

	// Spawn multiple readers (can run concurrently with writers)
	for i := range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 1000 {
				key := fmt.Sprintf("worker-%d-item-%d", id, j)
				_ = f.TestString(key)
			}
		}(i)
	}

	wg.Wait()
	fmt.Println("Items added:", f.Count())

	// Output:
	// Items added: 4000
}

// This example shows ShardedAtomicFilter for high-throughput concurrent writes.
func Example_sharded() {
	// ShardedAtomicFilter distributes writes across shards to reduce contention.
	// Use NewShardedAtomicDefault for auto-tuned shard count (based on GOMAXPROCS).
	f := gloom.NewShardedAtomicDefault(1_000_000, 0.01)

	var wg sync.WaitGroup

	// High-throughput parallel writes
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 10_000 {
				f.AddString(fmt.Sprintf("key-%d-%d", id, j))
			}
		}(i)
	}

	wg.Wait()
	fmt.Println("Total items:", f.Count())

	// Output:
	// Total items: 80000
}

// This example shows how to monitor filter statistics.
func Example_statistics() {
	f := gloom.New(10_000, 0.01)

	// Add some items
	for i := range 5000 {
		f.Add(fmt.Appendf(nil, "item-%d", i))
	}

	fmt.Printf("Capacity: %d bits\n", f.Cap())
	fmt.Printf("Hash functions (k): %d\n", f.K())
	fmt.Printf("Items added: %d\n", f.Count())
	fmt.Printf("Fill ratio: %.1f%%\n", f.EstimatedFillRatio()*100)

	// Output:
	// Capacity: 96256 bits
	// Hash functions (k): 7
	// Items added: 5000
	// Fill ratio: 30.4%
}

// This example demonstrates creating a filter with explicit parameters.
func Example_customParameters() {
	// Create with explicit block count and hash functions.
	// 1000 blocks * 512 bits = 512,000 bits total capacity.
	// k=7 hash functions is optimal for ~1% false positive rate.
	f := gloom.NewWithParams(1000, 7)

	f.AddString("custom")
	fmt.Println("Contains 'custom':", f.TestString("custom"))
	fmt.Printf("Blocks: %d, K: %d\n", f.NumBlocks(), f.K())

	// Output:
	// Contains 'custom': true
	// Blocks: 1000, K: 7
}

func ExampleNew() {
	// Create a filter sized for 1 million items with 1% false positive rate.
	// The filter automatically calculates optimal size and hash functions.
	f := gloom.New(1_000_000, 0.01)

	f.Add([]byte("hello"))
	fmt.Println(f.Test([]byte("hello")))

	// Output:
	// true
}

func ExampleNewAtomic() {
	// Create a thread-safe filter for concurrent access.
	f := gloom.NewAtomic(100_000, 0.01)

	// Safe to call from multiple goroutines
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		f.AddString("from-goroutine-1")
	}()

	go func() {
		defer wg.Done()
		f.AddString("from-goroutine-2")
	}()

	wg.Wait()
	fmt.Println("Count:", f.Count())

	// Output:
	// Count: 2
}

func ExampleNewShardedAtomic() {
	// Create a sharded filter with explicit shard count.
	// Use powers of 2 for optimal performance.
	f := gloom.NewShardedAtomic(100_000, 0.01, 16)

	f.AddString("key1")
	fmt.Println("Shards:", f.NumShards())

	// Output:
	// Shards: 16
}

func ExampleNewShardedAtomicDefault() {
	// Create a sharded filter with auto-tuned shard count.
	// Shard count is set to GOMAXPROCS (minimum 4).
	f := gloom.NewShardedAtomicDefault(100_000, 0.01)

	f.AddString("auto-tuned")
	fmt.Println("Has key:", f.TestString("auto-tuned"))

	// Output:
	// Has key: true
}

func ExampleOptimalParams() {
	// Calculate optimal parameters for your use case
	blocks, k, bitsPerItem := gloom.OptimalParams(1_000_000, 0.01)

	fmt.Printf("For 1M items at 1%% FP rate:\n")
	fmt.Printf("  Blocks: %d\n", blocks)
	fmt.Printf("  Hash functions (k): %d\n", k)
	fmt.Printf("  Bits per item: %.1f\n", bitsPerItem)

	// Output:
	// For 1M items at 1% FP rate:
	//   Blocks: 18721
	//   Hash functions (k): 7
	//   Bits per item: 9.6
}

func ExampleEstimateFalsePositiveRate() {
	// Estimate false positive rate for given parameters
	numBlocks := uint64(1000)
	k := uint32(7)
	itemsAdded := uint64(50000)

	rate := gloom.EstimateFalsePositiveRate(numBlocks, k, itemsAdded)
	fmt.Printf("Estimated FP rate: %.2f%%\n", rate*100)

	// Output:
	// Estimated FP rate: 0.86%
}
