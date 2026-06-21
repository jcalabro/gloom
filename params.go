package gloom

import "math"

const (
	// BlockBits is the number of bits per block (cache line size).
	BlockBits = 512
	// BlockWords is the number of uint64s per block.
	BlockWords = BlockBits / 64 // 8
	// ln2Squared is ln(2)^2.
	ln2Squared = 0.4804530139182014
)

// minK and maxK bound the supported number of partitions (hash functions). The upper
// bound is the largest k for which a partition of distinct, pairwise-coprime parts can
// still sum to exactly 512: the 18 smallest distinct primes already sum to 501, leaving
// too little room for an 18th distinct coprime part, so k=17 is the practical ceiling.
const (
	minK = 3
	maxK = 17
)

// primePartitions contains pre-computed partition configurations for different k values.
// Each configuration contains k values that are pairwise coprime and sum to exactly 512
// bits (the block size).
//
// The one-hashing technique requires the partition sizes to be PAIRWISE COPRIME (not
// merely distinct): only then does a single hash value, reduced modulo each size, yield
// independent positions (a Chinese Remainder Theorem argument). Most parts are primes,
// which are automatically pairwise coprime; where an even "filler" is needed to make an
// odd count of odd numbers sum to the even target 512, it is chosen coprime to the rest
// (2 itself is avoided as too small for good modulo distribution).
//
// The values are chosen to be:
//  1. Pairwise coprime (required for one-hashing independence)
//  2. Summing to exactly 512 to maximize block utilization
//  3. As large and near-equal as possible for good modulo distribution
//
// All entries are verified by tests to be pairwise coprime and to sum to 512.
var primePartitions = map[uint32][]uint32{
	3:  {167, 173, 172},                                                    // 172 is even filler
	4:  {109, 127, 137, 139},                                               // all prime
	5:  {97, 101, 103, 109, 102},                                           // 102 is even filler
	6:  {61, 79, 83, 89, 97, 103},                                          // all prime
	7:  {61, 67, 71, 79, 83, 89, 62},                                       // 62 is even filler
	8:  {37, 47, 53, 61, 67, 71, 79, 97},                                   // all prime
	9:  {41, 43, 47, 53, 59, 67, 71, 73, 58},                               // 58 is even filler
	10: {31, 37, 41, 43, 47, 53, 59, 61, 67, 73},                           // all prime
	11: {29, 31, 37, 41, 43, 44, 47, 53, 59, 61, 67},                       // 44 is even filler
	12: {17, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 71},                   // all prime
	13: {17, 19, 23, 29, 31, 37, 41, 43, 47, 52, 53, 59, 61},               // 52 is even filler
	14: {11, 13, 17, 19, 23, 29, 31, 37, 41, 47, 53, 59, 61, 71},           // all prime
	15: {11, 13, 17, 19, 23, 28, 29, 31, 37, 41, 43, 47, 53, 59, 61},       // 28 is even filler
	16: {5, 7, 11, 13, 17, 19, 23, 27, 29, 31, 37, 41, 43, 47, 53, 109},    // 27 = 3^3, coprime to rest
	17: {3, 5, 7, 11, 13, 17, 19, 23, 29, 31, 32, 37, 41, 43, 47, 53, 101}, // 32 = 2^5, coprime to rest
}

// blockingGrowthCap bounds how far OptimalParams will grow the block count beyond the
// classic estimate while compensating for the cache-line blocking penalty. The penalty is
// modest in the practical range (well under 2x extra bits up to very low FP targets), so a
// 4x cap is never reached by attainable targets; it only bounds work for targets that the
// 512-bit block structure cannot meet at any size.
const blockingGrowthCap = 4.0

// OptimalParams calculates the optimal bloom filter parameters.
// Returns the number of blocks, number of hash functions (k), and the classic
// (non-blocked) bits per item used as the starting estimate.
//
// The sizing accounts for two effects that the textbook formulas ignore and that would
// otherwise leave the realized rate worse than requested:
//
//   - Cache-line blocking penalty: items land in 512-bit blocks following a Poisson
//     distribution, and because per-block FP is convex in load, the average realized FP
//     exceeds the value the global-filter formula predicts. OptimalParams grows the block
//     count until the estimated rate actually meets the target (bounded by
//     blockingGrowthCap).
//   - k selection: a large k splits each block into tiny prime partitions whose modulo
//     collisions inflate FP, so the FP-minimizing k is chosen by [bestK] rather than the
//     classic (m/n)*ln(2) value.
//
// For target rates too low to achieve with a 512-bit block at any size, the block count is
// capped and the realized rate will exceed the target; callers needing a guarantee should
// consult [AchievableFalsePositiveRate].
func OptimalParams(expectedItems uint64, fpRate float64) (numBlocks uint64, k uint32, bitsPerItem float64) {
	if expectedItems == 0 {
		expectedItems = 1
	}
	if fpRate <= 0 {
		fpRate = 0.0001 // default to 0.01%
	}
	if fpRate >= 1 {
		fpRate = 0.99
	}

	// Classic (non-blocked) bits per item: -ln(fpRate) / ln(2)^2. This is the lower bound;
	// the blocking penalty means we usually need somewhat more.
	bitsPerItem = -math.Log(fpRate) / ln2Squared

	// Starting block count from the classic estimate, rounded up to a whole block. With
	// expectedItems >= 1 and fpRate < 1, bitsPerItem > 0, so this is always >= 1.
	totalBits := float64(expectedItems) * bitsPerItem
	baseBlocks := uint64(math.Ceil(totalBits / BlockBits))
	maxBlocks := uint64(math.Ceil(float64(baseBlocks) * blockingGrowthCap))

	// Grow the block count until the FP-minimizing k actually meets the target, or the cap
	// is reached. Growth is geometric for speed, then we keep the smallest block count that
	// satisfies the target.
	numBlocks = baseBlocks
	k = bestK(numBlocks, expectedItems)
	for EstimateFalsePositiveRate(numBlocks, k, expectedItems) > fpRate && numBlocks < maxBlocks {
		next := min(numBlocks+numBlocks/8+1, maxBlocks) // ~12.5% growth per step
		numBlocks = next
		k = bestK(numBlocks, expectedItems)
	}

	return numBlocks, k, bitsPerItem
}

// bestK returns the supported k in [minK, maxK] that minimizes the estimated
// false-positive rate for the given block count and item count.
func bestK(numBlocks, expectedItems uint64) uint32 {
	best := uint32(minK)
	bestFP := math.Inf(1)
	for k := uint32(minK); k <= maxK; k++ {
		fp := EstimateFalsePositiveRate(numBlocks, k, expectedItems)
		if fp < bestFP {
			bestFP = fp
			best = k
		}
	}
	return best
}

// AchievableFalsePositiveRate returns the lowest false-positive rate the filter can
// actually deliver for expectedItems, and whether that meets the requested fpRate.
//
// The number of hash functions k is capped at maxK because a 512-bit block cannot be
// partitioned into more than maxK distinct, pairwise-coprime parts. For very low target
// rates the optimal k exceeds this cap, so the realized rate is worse than requested no
// matter how much memory is allocated. This function makes that shortfall observable:
// callers that require a hard guarantee can check met and react (allocate differently,
// shard, or accept the achievable rate) rather than silently shipping a worse rate.
//
// achievable is computed with the same blocked, partitioned model used by
// [EstimateFalsePositiveRate], so it reflects the rate the filter will actually exhibit
// at the given load, including the cache-line blocking penalty.
func AchievableFalsePositiveRate(expectedItems uint64, fpRate float64) (achievable float64, met bool) {
	numBlocks, k, _ := OptimalParams(expectedItems, fpRate)
	achievable = EstimateFalsePositiveRate(numBlocks, k, expectedItems)
	// Treat tiny floating-point overshoot as met; the requested rate is the contract.
	met = achievable <= fpRate*(1+1e-9)
	return achievable, met
}

// GetPrimePartition returns the prime partition for the given k value.
// Returns nil if k is not supported.
func GetPrimePartition(k uint32) []uint32 {
	return primePartitions[k]
}

// ComputeOffsets computes the cumulative bit offsets for each partition.
// offset[i] = sum of primes[0..i-1]
func ComputeOffsets(primes []uint32) []uint32 {
	offsets := make([]uint32, len(primes))
	var cumulative uint32
	for i, p := range primes {
		offsets[i] = cumulative
		cumulative += p
	}
	return offsets
}

// partitionedBlockFP computes the false positive rate for a single block
// containing j items using the partitioned one-hashing scheme.
//
// Each partition i has size primes[i], and j items each set one bit uniformly
// in that partition. The probability that a random query finds partition i's
// probe bit already set is 1 - (1 - 1/p_i)^j. Since partitions are independent,
// the overall FP rate is the product across all partitions.
func partitionedBlockFP(primes []uint32, j float64) float64 {
	fp := 1.0
	for _, p := range primes {
		fp *= 1 - math.Pow(1-1/float64(p), j)
	}
	return fp
}

// EstimateFalsePositiveRate estimates the false positive rate for given parameters.
//
// For a cache-line blocked bloom filter, items are distributed across blocks
// following a Poisson distribution (balls-into-bins). Some blocks receive more
// items than average, increasing their local FP rate. This function computes
// the expected per-block FP rate over this Poisson distribution using the
// partitioned formula that accounts for the actual prime partition sizes:
//
//	FP = E[∏ᵢ (1 - (1 - 1/pᵢ)^J)]  where J ~ Poisson(n/B)
//
// This is more accurate than the standard formula (1 - e^(-kn/m))^k, which
// assumes uniform bit placement across the entire block and underestimates
// the FP rate of partitioned blocked filters.
func EstimateFalsePositiveRate(numBlocks uint64, k uint32, itemsAdded uint64) float64 {
	if numBlocks == 0 || itemsAdded == 0 {
		return 0
	}

	primes := GetPrimePartition(k)
	lambda := float64(itemsAdded) / float64(numBlocks) // expected items per block

	// For very large lambda, the Poisson variance relative to the mean is
	// negligible and we can evaluate directly at the mean.
	if lambda > 10000 {
		if primes != nil {
			return partitionedBlockFP(primes, lambda)
		}
		// Fallback for unsupported k values
		s := float64(BlockBits)
		kf := float64(k)
		m := float64(numBlocks) * s
		return math.Pow(1-math.Exp(-kf*float64(itemsAdded)/m), kf)
	}

	// Compute Poisson-weighted sum: sum over j of P(J=j) * blockFP(j)
	// Use log-space for Poisson probabilities to avoid overflow/underflow.
	maxJ := int(lambda + 10*math.Sqrt(lambda) + 20)
	var fp float64
	var logFactorial float64 // log(j!)
	logLambda := math.Log(lambda)

	// Precompute fallback values for unsupported k
	kf := float64(k)
	s := float64(BlockBits)

	for j := 0; j <= maxJ; j++ {
		if j > 0 {
			logFactorial += math.Log(float64(j))
		}

		// log(P(J=j)) = -lambda + j*log(lambda) - log(j!)
		logProb := -lambda + float64(j)*logLambda - logFactorial
		prob := math.Exp(logProb)

		if prob < 1e-15 && j > int(lambda) {
			break
		}

		// Per-block FP rate with j items.
		// When j=0 this is 0, skip to avoid unnecessary computation.
		if j > 0 {
			var blockFP float64
			if primes != nil {
				blockFP = partitionedBlockFP(primes, float64(j))
			} else {
				blockFP = math.Pow(1-math.Exp(-kf*float64(j)/s), kf)
			}
			fp += prob * blockFP
		}
	}

	return fp
}
