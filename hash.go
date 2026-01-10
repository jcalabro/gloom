package gloom

import "github.com/zeebo/xxh3"

// hashData computes the xxh3 hash of the given data and returns
// the block index (upper 32 bits) and intra-block hash (lower 32 bits).
func hashData(data []byte, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	h := xxh3.Hash(data)
	return hashSplit(h, numBlocks)
}

// hashString computes the xxh3 hash of the given string and returns
// the block index (upper 32 bits) and intra-block hash (lower 32 bits).
// This avoids the allocation of converting string to []byte.
func hashString(s string, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	h := xxh3.HashString(s)
	return hashSplit(h, numBlocks)
}

// hashSplit splits a 64-bit hash into block index and intra-block hash.
func hashSplit(h uint64, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	// Use upper 32 bits for block selection (better distribution)
	blockIdx = (h >> 32) % numBlocks
	// Use lower 32 bits for intra-block hashing
	intraHash = uint32(h)
	return
}

// hashRaw returns the raw 64-bit hash of data.
func hashRaw(data []byte) uint64 {
	return xxh3.Hash(data)
}

// hashRawString returns the raw 64-bit hash of a string.
func hashRawString(s string) uint64 {
	return xxh3.HashString(s)
}

// hashSplitWithHash splits a pre-computed hash into block index and intra-block hash.
func hashSplitWithHash(h uint64, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	return hashSplit(h, numBlocks)
}
