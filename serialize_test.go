package gloom

import (
	"bytes"
	"fmt"
	"testing"
)

func TestSerializeRoundtripEmpty(t *testing.T) {
	// Test roundtrip with empty filter
	original := New(1000, 0.01)

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Verify parameters match
	if restored.NumBlocks() != original.NumBlocks() {
		t.Errorf("NumBlocks mismatch: got %d, want %d", restored.NumBlocks(), original.NumBlocks())
	}
	if restored.K() != original.K() {
		t.Errorf("K mismatch: got %d, want %d", restored.K(), original.K())
	}
	if restored.Count() != original.Count() {
		t.Errorf("Count mismatch: got %d, want %d", restored.Count(), original.Count())
	}
	if restored.Cap() != original.Cap() {
		t.Errorf("Cap mismatch: got %d, want %d", restored.Cap(), original.Cap())
	}
	if restored.EstimatedFillRatio() != original.EstimatedFillRatio() {
		t.Errorf("EstimatedFillRatio mismatch: got %f, want %f", restored.EstimatedFillRatio(), original.EstimatedFillRatio())
	}
}

func TestSerializeRoundtripWithData(t *testing.T) {
	original := New(10000, 0.01)

	// Add items
	items := []string{"hello", "world", "foo", "bar", "baz", "qux"}
	for _, item := range items {
		original.AddString(item)
	}

	// Add more items
	for i := range 1000 {
		original.Add(fmt.Appendf(nil, "item-%d", i))
	}

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Verify parameters match
	if restored.NumBlocks() != original.NumBlocks() {
		t.Errorf("NumBlocks mismatch: got %d, want %d", restored.NumBlocks(), original.NumBlocks())
	}
	if restored.K() != original.K() {
		t.Errorf("K mismatch: got %d, want %d", restored.K(), original.K())
	}
	if restored.Count() != original.Count() {
		t.Errorf("Count mismatch: got %d, want %d", restored.Count(), original.Count())
	}

	// Verify all added items are still present (no false negatives)
	for _, item := range items {
		if !restored.TestString(item) {
			t.Errorf("false negative for %q after deserialization", item)
		}
	}
	for i := range 1000 {
		key := fmt.Appendf(nil, "item-%d", i)
		if !restored.Test(key) {
			t.Errorf("false negative for item-%d after deserialization", i)
		}
	}

	// Verify fill ratio matches
	if restored.EstimatedFillRatio() != original.EstimatedFillRatio() {
		t.Errorf("EstimatedFillRatio mismatch: got %f, want %f", restored.EstimatedFillRatio(), original.EstimatedFillRatio())
	}
}

func TestSerializeRoundtripAllKValues(t *testing.T) {
	// Test serialization with all supported k values
	for k := uint32(3); k <= 14; k++ {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			original := NewWithParams(100, k)

			// Add items
			for i := range 500 {
				original.AddString(fmt.Sprintf("k%d-item-%d", k, i))
			}

			data, err := original.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary failed: %v", err)
			}

			restored, err := UnmarshalBinary(data)
			if err != nil {
				t.Fatalf("UnmarshalBinary failed: %v", err)
			}

			// Verify k matches
			if restored.K() != k {
				t.Errorf("K mismatch: got %d, want %d", restored.K(), k)
			}

			// Verify all items are still present
			for i := range 500 {
				if !restored.TestString(fmt.Sprintf("k%d-item-%d", k, i)) {
					t.Errorf("false negative for k%d-item-%d", k, i)
				}
			}
		})
	}
}

func TestSerializeRoundtripVariousSizes(t *testing.T) {
	sizes := []struct {
		items  uint64
		fpRate float64
	}{
		{10, 0.1},
		{100, 0.01},
		{1000, 0.01},
		{10000, 0.001},
		{100000, 0.0001},
	}

	for _, tc := range sizes {
		t.Run(fmt.Sprintf("items=%d_fp=%.4f", tc.items, tc.fpRate), func(t *testing.T) {
			original := New(tc.items, tc.fpRate)

			// Add half the expected items
			itemsToAdd := tc.items / 2
			for i := range itemsToAdd {
				original.Add(fmt.Appendf(nil, "size-test-%d", i))
			}

			data, err := original.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary failed: %v", err)
			}

			restored, err := UnmarshalBinary(data)
			if err != nil {
				t.Fatalf("UnmarshalBinary failed: %v", err)
			}

			// Verify parameters
			if restored.NumBlocks() != original.NumBlocks() {
				t.Errorf("NumBlocks mismatch: got %d, want %d", restored.NumBlocks(), original.NumBlocks())
			}
			if restored.Count() != original.Count() {
				t.Errorf("Count mismatch: got %d, want %d", restored.Count(), original.Count())
			}

			// Verify all items are present
			for i := range itemsToAdd {
				key := fmt.Appendf(nil, "size-test-%d", i)
				if !restored.Test(key) {
					t.Errorf("false negative for item %d", i)
				}
			}
		})
	}
}

func TestSerializeDataTooShort(t *testing.T) {
	// Data shorter than header
	shortData := make([]byte, headerSize-1)
	_, err := UnmarshalBinary(shortData)
	if err == nil {
		t.Error("expected error for short data")
	}

	// Empty data
	_, err = UnmarshalBinary([]byte{})
	if err == nil {
		t.Error("expected error for empty data")
	}

	// Nil data
	_, err = UnmarshalBinary(nil)
	if err == nil {
		t.Error("expected error for nil data")
	}
}

func TestSerializeUnsupportedVersion(t *testing.T) {
	// Create valid data then modify version
	f := New(100, 0.01)
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Change version to unsupported value
	data[0] = 0
	_, err = UnmarshalBinary(data)
	if err == nil {
		t.Error("expected error for version 0")
	}

	data[0] = 2
	_, err = UnmarshalBinary(data)
	if err == nil {
		t.Error("expected error for version 2")
	}

	data[0] = 255
	_, err = UnmarshalBinary(data)
	if err == nil {
		t.Error("expected error for version 255")
	}
}

func TestSerializeInvalidK(t *testing.T) {
	// Create valid data then modify k
	f := NewWithParams(100, 7)
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Test invalid k values
	invalidKValues := []uint32{0, 1, 2, 15, 16, 100, 255}
	for _, invalidK := range invalidKValues {
		dataCopy := make([]byte, len(data))
		copy(dataCopy, data)

		// Write invalid k (little-endian uint32 at offset 1)
		dataCopy[1] = byte(invalidK)
		dataCopy[2] = byte(invalidK >> 8)
		dataCopy[3] = byte(invalidK >> 16)
		dataCopy[4] = byte(invalidK >> 24)

		_, err := UnmarshalBinary(dataCopy)
		if err == nil {
			t.Errorf("expected error for k=%d", invalidK)
		}
	}
}

func TestSerializeDataLengthMismatch(t *testing.T) {
	f := New(100, 0.01)
	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Truncate data
	truncated := data[:len(data)-1]
	_, err = UnmarshalBinary(truncated)
	if err == nil {
		t.Error("expected error for truncated data")
	}

	// Extra data
	extended := append(data, 0xFF)
	_, err = UnmarshalBinary(extended)
	if err == nil {
		t.Error("expected error for extended data")
	}

	// Much shorter data (header + 1 byte)
	muchShorter := data[:headerSize+1]
	_, err = UnmarshalBinary(muchShorter)
	if err == nil {
		t.Error("expected error for much shorter data")
	}
}

func TestSerializeZeroNumBlocks(t *testing.T) {
	// Manually craft data with numBlocks=0
	data := make([]byte, headerSize)
	data[0] = 1                                     // version
	data[1], data[2], data[3], data[4] = 7, 0, 0, 0 // k=7
	// bytes 5-12 are all 0 (numBlocks=0)
	// bytes 13-20 are all 0 (count=0)

	_, err := UnmarshalBinary(data)
	if err == nil {
		t.Error("expected error for numBlocks=0")
	}
}

func TestSerializeNumBlocksTooLarge(t *testing.T) {
	// Manually craft data with huge numBlocks that would cause overflow
	data := make([]byte, headerSize)
	data[0] = 1                                     // version
	data[1], data[2], data[3], data[4] = 7, 0, 0, 0 // k=7
	// Set numBlocks to a huge value (0x3000000000000000)
	data[5] = 0
	data[6] = 0
	data[7] = 0
	data[8] = 0
	data[9] = 0
	data[10] = 0
	data[11] = 0
	data[12] = 0x30 // This makes numBlocks = 0x3000000000000000 in little-endian

	_, err := UnmarshalBinary(data)
	if err == nil {
		t.Error("expected error for huge numBlocks")
	}
}

func TestSerializeBlockDataIntegrity(t *testing.T) {
	// Test that bit-level data is preserved exactly
	original := NewWithParams(10, 7) // Small filter for easy verification

	// Add specific items
	testItems := []string{"alpha", "beta", "gamma", "delta"}
	for _, item := range testItems {
		original.AddString(item)
	}

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Verify blocks are identical by checking fill ratio and specific bits
	if original.EstimatedFillRatio() != restored.EstimatedFillRatio() {
		t.Errorf("fill ratio mismatch: original=%f, restored=%f",
			original.EstimatedFillRatio(), restored.EstimatedFillRatio())
	}

	// Test a large number of random keys - the false positive pattern should match
	matches := 0
	for i := range 10000 {
		key := fmt.Appendf(nil, "random-key-%d", i)
		origResult := original.Test(key)
		restoredResult := restored.Test(key)
		if origResult == restoredResult {
			matches++
		}
	}

	// All results should match (both true and false)
	if matches != 10000 {
		t.Errorf("result mismatch: only %d/10000 keys had matching results", matches)
	}
}

func TestSerializeCanAddAfterDeserialize(t *testing.T) {
	// Test that we can continue using the filter after deserialization
	original := New(10000, 0.01)

	// Add initial items
	for i := range 500 {
		original.AddString(fmt.Sprintf("initial-%d", i))
	}

	data, err := original.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Add more items to the restored filter
	for i := range 500 {
		restored.AddString(fmt.Sprintf("new-%d", i))
	}

	// Verify original items are still present
	for i := range 500 {
		if !restored.TestString(fmt.Sprintf("initial-%d", i)) {
			t.Errorf("false negative for initial-%d", i)
		}
	}

	// Verify new items are present
	for i := range 500 {
		if !restored.TestString(fmt.Sprintf("new-%d", i)) {
			t.Errorf("false negative for new-%d", i)
		}
	}

	// Count should reflect all additions
	if restored.Count() != 1000 {
		t.Errorf("expected count 1000, got %d", restored.Count())
	}
}

func TestSerializeMultipleRoundtrips(t *testing.T) {
	// Test that multiple serialize/deserialize cycles preserve data
	f := New(1000, 0.01)

	// Add items
	for i := range 100 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	// Do multiple roundtrips
	for round := range 5 {
		data, err := f.MarshalBinary()
		if err != nil {
			t.Fatalf("round %d: MarshalBinary failed: %v", round, err)
		}

		f, err = UnmarshalBinary(data)
		if err != nil {
			t.Fatalf("round %d: UnmarshalBinary failed: %v", round, err)
		}

		// Verify data after each round
		for i := range 100 {
			if !f.TestString(fmt.Sprintf("item-%d", i)) {
				t.Errorf("round %d: false negative for item-%d", round, i)
			}
		}
	}
}

func TestSerializeDataFormat(t *testing.T) {
	// Test that the serialized format is as expected
	f := NewWithParams(2, 5) // 2 blocks, k=5

	// Add an item to set some bits
	f.AddString("test")

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Verify header
	if data[0] != serializeVersion {
		t.Errorf("version mismatch: got %d, want %d", data[0], serializeVersion)
	}

	// Verify k (little-endian uint32 at offset 1)
	k := uint32(data[1]) | uint32(data[2])<<8 | uint32(data[3])<<16 | uint32(data[4])<<24
	if k != 5 {
		t.Errorf("k mismatch: got %d, want 5", k)
	}

	// Verify numBlocks (little-endian uint64 at offset 5)
	numBlocks := uint64(data[5]) | uint64(data[6])<<8 | uint64(data[7])<<16 | uint64(data[8])<<24 |
		uint64(data[9])<<32 | uint64(data[10])<<40 | uint64(data[11])<<48 | uint64(data[12])<<56
	if numBlocks != 2 {
		t.Errorf("numBlocks mismatch: got %d, want 2", numBlocks)
	}

	// Verify count (little-endian uint64 at offset 13)
	count := uint64(data[13]) | uint64(data[14])<<8 | uint64(data[15])<<16 | uint64(data[16])<<24 |
		uint64(data[17])<<32 | uint64(data[18])<<40 | uint64(data[19])<<48 | uint64(data[20])<<56
	if count != 1 {
		t.Errorf("count mismatch: got %d, want 1", count)
	}

	// Verify total length
	expectedLen := headerSize + 2*BlockWords*8 // 2 blocks * 8 words * 8 bytes
	if len(data) != expectedLen {
		t.Errorf("data length mismatch: got %d, want %d", len(data), expectedLen)
	}
}

func TestSerializeCacheLineAlignment(t *testing.T) {
	// Test that deserialized filter maintains cache-line alignment
	f := New(1000, 0.01)
	for i := range 100 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Verify blocks are cache-line aligned (64 bytes)
	addr := uintptr(unsafePointer(&restored.blocks[0]))
	if addr%64 != 0 {
		t.Errorf("restored blocks not 64-byte aligned: address %x", addr)
	}
}

func TestSerializeEmptyBlocks(t *testing.T) {
	// Test filter with minimum size (1 block)
	f := NewWithParams(1, 7)

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if restored.NumBlocks() != 1 {
		t.Errorf("expected 1 block, got %d", restored.NumBlocks())
	}

	// Should be able to add items
	restored.AddString("test")
	if !restored.TestString("test") {
		t.Error("false negative for 'test'")
	}
}

func TestSerializeLargeFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large filter test in short mode")
	}

	// Test with a large filter
	f := New(1000000, 0.001)

	// Add many items
	for i := range 100000 {
		f.Add(fmt.Appendf(nil, "large-item-%d", i))
	}

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	t.Logf("Large filter serialized size: %d bytes (%.2f MB)", len(data), float64(len(data))/1024/1024)

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	// Verify a sample of items
	for i := 0; i < 100000; i += 1000 {
		key := fmt.Appendf(nil, "large-item-%d", i)
		if !restored.Test(key) {
			t.Errorf("false negative for large-item-%d", i)
		}
	}
}

func TestSerializeIdempotent(t *testing.T) {
	// Test that serializing the same filter twice produces identical data
	f := New(1000, 0.01)
	for i := range 100 {
		f.AddString(fmt.Sprintf("item-%d", i))
	}

	data1, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("first MarshalBinary failed: %v", err)
	}

	data2, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("second MarshalBinary failed: %v", err)
	}

	if !bytes.Equal(data1, data2) {
		t.Error("serialization is not idempotent")
	}
}

func TestSerializeMinimalFilter(t *testing.T) {
	// Test with smallest possible parameters
	f := NewWithParams(1, 3) // 1 block, k=3

	data, err := f.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary failed: %v", err)
	}

	// Expected size: header (21) + 1 block * 8 words * 8 bytes = 21 + 64 = 85 bytes
	expectedSize := headerSize + 1*BlockWords*8
	if len(data) != expectedSize {
		t.Errorf("unexpected data size: got %d, want %d", len(data), expectedSize)
	}

	restored, err := UnmarshalBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalBinary failed: %v", err)
	}

	if restored.K() != 3 {
		t.Errorf("k mismatch: got %d, want 3", restored.K())
	}
	if restored.NumBlocks() != 1 {
		t.Errorf("numBlocks mismatch: got %d, want 1", restored.NumBlocks())
	}
}

func TestSerializeNoFalseNegativesProperty(t *testing.T) {
	// Property test: after roundtrip, there should never be false negatives
	testCases := []struct {
		items  uint64
		fpRate float64
		k      uint32
	}{
		{100, 0.1, 0}, // k=0 means use optimal
		{1000, 0.01, 0},
		{10000, 0.001, 0},
		{100, 0.01, 3},  // minimum k
		{100, 0.01, 14}, // maximum k
	}

	for _, tc := range testCases {
		name := fmt.Sprintf("items=%d_fp=%.3f_k=%d", tc.items, tc.fpRate, tc.k)
		t.Run(name, func(t *testing.T) {
			var original *Filter
			if tc.k == 0 {
				original = New(tc.items, tc.fpRate)
			} else {
				numBlocks, _, _ := OptimalParams(tc.items, tc.fpRate)
				original = NewWithParams(numBlocks, tc.k)
			}

			// Add items
			itemCount := min(tc.items, 1000) // Cap at 1000 for test speed
			for i := range itemCount {
				original.Add(fmt.Appendf(nil, "prop-%d", i))
			}

			data, err := original.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary failed: %v", err)
			}

			restored, err := UnmarshalBinary(data)
			if err != nil {
				t.Fatalf("UnmarshalBinary failed: %v", err)
			}

			// Check for false negatives
			for i := range itemCount {
				key := fmt.Appendf(nil, "prop-%d", i)
				if !restored.Test(key) {
					t.Errorf("false negative for item %d", i)
				}
			}
		})
	}
}

// FuzzSerializeRoundtrip tests that any valid filter can be roundtripped
func FuzzSerializeRoundtrip(f *testing.F) {
	// Seed with various configurations
	f.Add(uint64(100), uint32(7), "hello")
	f.Add(uint64(1000), uint32(3), "world")
	f.Add(uint64(10), uint32(14), "test")

	f.Fuzz(func(t *testing.T, numBlocks uint64, k uint32, item string) {
		// Constrain to valid ranges
		if numBlocks == 0 || numBlocks > 10000 {
			numBlocks = 100
		}
		if k < 3 || k > 14 {
			k = 7
		}

		filter := NewWithParams(numBlocks, k)
		filter.AddString(item)

		data, err := filter.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary failed: %v", err)
		}

		restored, err := UnmarshalBinary(data)
		if err != nil {
			t.Fatalf("UnmarshalBinary failed: %v", err)
		}

		// Must not have false negatives
		if !restored.TestString(item) {
			t.Errorf("false negative for %q", item)
		}

		// Parameters must match
		if restored.K() != filter.K() {
			t.Errorf("K mismatch: got %d, want %d", restored.K(), filter.K())
		}
		if restored.NumBlocks() != filter.NumBlocks() {
			t.Errorf("NumBlocks mismatch: got %d, want %d", restored.NumBlocks(), filter.NumBlocks())
		}
	})
}

// FuzzUnmarshalBinaryInvalid tests that invalid data doesn't cause panics
func FuzzUnmarshalBinaryInvalid(f *testing.F) {
	// Seed with some invalid data patterns
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add([]byte{1, 0, 0, 0, 0})
	f.Add(make([]byte, headerSize))

	// Add some valid data to mutate
	filter := New(100, 0.01)
	filter.AddString("test")
	validData, _ := filter.MarshalBinary()
	f.Add(validData)

	f.Fuzz(func(t *testing.T, data []byte) {
		// Should not panic, may return error
		_, _ = UnmarshalBinary(data)
	})
}
