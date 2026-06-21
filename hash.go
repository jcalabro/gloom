package gloom

import (
	"math/bits"

	"github.com/zeebo/xxh3"
)

// We hash each key once with xxh3's 128-bit variant and split the result into two
// independent halves:
//
//   - Hi  drives block (and, for sharded filters, shard) selection.
//   - Lo  is folded into the 32-bit intra-block hash fed to the prime-modulo probes.
//
// Using disjoint halves removes two problems present in the original 64-bit design:
// (1) block selection is no longer limited to a narrow bit slice (the sharded path
// previously used only 16 bits, capping it at 65536 reachable blocks and silently
// destroying the false-positive rate past that point), and (2) the intra-block hash is
// derived from all 64 low bits rather than from a slice that overlapped block selection.
//
// Block selection uses Lemire's multiply-shift reduction rather than a modulo, which is
// both faster (no hardware divide) and free of modulo bias for any block count.
//
// The intra-block hash is reduced to 32 bits by folding the two halves of Lo together.
// The prime-modulo probes then divide a 32-bit value, which compiles to a 32-bit DIVL
// rather than a 64-bit DIVQ; the narrower divide is measurably faster, and the realized
// false-positive rate is unchanged because the partition sizes (<= 512) leave 32 bits of
// dividend entropy far in excess of what the modulo consumes.

// reduceRange maps a uniformly distributed 64-bit value into [0, n) via the
// multiply-shift reduction floor(h * n / 2^64). For uniform h the result is uniform over
// [0, n) up to a bias bounded by n / 2^64, which is negligible for any realistic n.
// Returns 0 when n == 0 (callers guarantee n >= 1).
func reduceRange(h, n uint64) uint64 {
	hi, _ := bits.Mul64(h, n)
	return hi
}

// foldIntra reduces the 64-bit low half to a 32-bit intra-block hash, mixing both 32-bit
// lanes so no entropy from Lo is discarded.
func foldIntra(lo uint64) uint32 {
	return uint32(lo) ^ uint32(lo>>32)
}

// hashData hashes data and returns the block index and 32-bit intra-block hash.
func hashData(data []byte, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	h := xxh3.Hash128(data)
	return reduceRange(h.Hi, numBlocks), foldIntra(h.Lo)
}

// hashString hashes a string without allocating and returns the block index and
// 32-bit intra-block hash.
func hashString(s string, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	h := xxh3.HashString128(s)
	return reduceRange(h.Hi, numBlocks), foldIntra(h.Lo)
}

// hashRaw128 returns the raw 128-bit hash of data, used by the sharded filter which
// needs to derive a shard index before selecting a block within that shard.
func hashRaw128(data []byte) xxh3.Uint128 {
	return xxh3.Hash128(data)
}

// hashRawString128 returns the raw 128-bit hash of a string without allocating.
func hashRawString128(s string) xxh3.Uint128 {
	return xxh3.HashString128(s)
}

// shardIndexFromHash selects a shard from the low bits of the high half. Block selection
// within the shard uses the multiply-shift reduction of the same high half, which is
// dominated by its high bits, so shard and block selection draw on effectively disjoint
// regions of the 64-bit value and are uncorrelated in practice.
func shardIndexFromHash(h xxh3.Uint128, mask uint64) uint64 {
	return h.Hi & mask
}

// hashSplitSharded splits a pre-computed 128-bit hash into a per-shard block index and
// the 32-bit intra-block hash.
func hashSplitSharded(h xxh3.Uint128, numBlocks uint64) (blockIdx uint64, intraHash uint32) {
	return reduceRange(h.Hi, numBlocks), foldIntra(h.Lo)
}
