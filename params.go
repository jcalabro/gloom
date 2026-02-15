package gloom

import "math"

const (
	// BlockBits is the number of bits per block (cache line size).
	BlockBits = 512
	// BlockWords is the number of uint64s per block.
	BlockWords = BlockBits / 64 // 8
	// ln2 is the natural logarithm of 2.
	ln2 = 0.6931471805599453
	// ln2Squared is ln(2)^2.
	ln2Squared = 0.4804530139182014
)

// primePartitions contains pre-computed partition configurations for different
// k values. Each configuration contains k strictly distinct values that sum to
// exactly 512 bits (the block size).
//
// For even k values, all values are distinct primes.
// For odd k values, one value must be even (and thus non-prime, since 2 is too
// small for good modulo distribution) because the sum of an odd count of odd
// numbers is always odd, but the target sum (512) is even.
//
// The values are chosen to be:
// 1. Strictly distinct (required for one-hashing independence)
// 2. Sum to exactly 512 to maximize block utilization
// 3. As large as possible for good modulo distribution
var primePartitions = map[uint32][]uint32{
	3:  {167, 173, 172},                                          // sum = 512 (172 is even filler)
	4:  {109, 127, 137, 139},                                     // sum = 512, all prime
	5:  {97, 101, 103, 109, 102},                                 // sum = 512 (102 is even filler)
	6:  {61, 79, 83, 89, 97, 103},                                // sum = 512, all prime
	7:  {61, 67, 71, 79, 83, 89, 62},                             // sum = 512 (62 is even filler)
	8:  {37, 47, 53, 61, 67, 71, 79, 97},                         // sum = 512, all prime
	9:  {41, 43, 47, 53, 59, 67, 71, 73, 58},                     // sum = 512 (58 is even filler)
	10: {31, 37, 41, 43, 47, 53, 59, 61, 67, 73},                 // sum = 512, all prime
	11: {29, 31, 37, 41, 43, 44, 47, 53, 59, 61, 67},             // sum = 512 (44 is even filler)
	12: {17, 23, 29, 31, 37, 41, 43, 47, 53, 59, 61, 71},         // sum = 512, all prime
	13: {17, 19, 23, 29, 31, 37, 41, 43, 47, 52, 53, 59, 61},     // sum = 512 (52 is even filler)
	14: {11, 13, 17, 19, 23, 29, 31, 37, 41, 47, 53, 59, 61, 71}, // sum = 512, all prime
}

// OptimalParams calculates the optimal bloom filter parameters.
// Returns the number of blocks, number of hash functions (k), and bits per item.
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

	// Optimal bits per item: -ln(fpRate) / ln(2)^2
	bitsPerItem = -math.Log(fpRate) / ln2Squared

	// Total bits needed
	totalBits := float64(expectedItems) * bitsPerItem

	// Round up to nearest block (always >= 1 since totalBits > 0)
	numBlocks = uint64(math.Ceil(totalBits / BlockBits))

	// Actual bits per item given block rounding
	actualBitsPerItem := float64(numBlocks*BlockBits) / float64(expectedItems)

	// Optimal k: (m/n) * ln(2) = bitsPerItem * ln(2)
	kFloat := actualBitsPerItem * ln2
	k = uint32(math.Round(kFloat))

	// Clamp k to supported range
	k = max(k, 3)
	k = min(k, 14)

	return numBlocks, k, bitsPerItem
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

// EstimateFalsePositiveRate estimates the false positive rate for given parameters.
//
// For a cache-line blocked bloom filter, items are distributed across blocks
// following a Poisson distribution (balls-into-bins). Some blocks receive more
// items than average, increasing their local FP rate. This function computes
// the expected per-block FP rate over this Poisson distribution:
//
//	FP = E[(1 - e^(-k*J/s))^k]  where J ~ Poisson(n/B), s = BlockBits
//
// This gives a more accurate estimate than the standard formula (1 - e^(-kn/m))^k,
// which assumes uniform bit placement and underestimates the FP rate of blocked filters.
func EstimateFalsePositiveRate(numBlocks uint64, k uint32, itemsAdded uint64) float64 {
	if numBlocks == 0 || itemsAdded == 0 {
		return 0
	}

	lambda := float64(itemsAdded) / float64(numBlocks) // expected items per block
	s := float64(BlockBits)
	kf := float64(k)

	// For very large lambda, the Poisson variance relative to the mean is
	// negligible and the standard formula is a good approximation.
	if lambda > 10000 {
		m := float64(numBlocks) * s
		return math.Pow(1-math.Exp(-kf*float64(itemsAdded)/m), kf)
	}

	// Compute Poisson-weighted sum: sum over j of P(J=j) * (1 - e^(-k*j/s))^k
	// Use log-space for Poisson probabilities to avoid overflow/underflow.
	maxJ := int(lambda + 10*math.Sqrt(lambda) + 20)
	var fp float64
	var logFactorial float64 // log(j!)
	logLambda := math.Log(lambda)

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

		// Per-block FP rate with j items: (1 - e^(-k*j/s))^k
		// When j=0 this is 0, skip to avoid unnecessary computation.
		if j > 0 {
			fp += prob * math.Pow(1-math.Exp(-kf*float64(j)/s), kf)
		}
	}

	return fp
}
