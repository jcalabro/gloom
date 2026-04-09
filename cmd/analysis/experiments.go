package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/jcalabro/gloom"
)

const (
	defaultTrials   = 10_000
	defaultTestKeys = 10_000
)

func progress(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

type filterOps struct {
	add  func(key []byte)
	test func(key []byte) bool
}

type filterFactory func() filterOps

func makeFilterFactory(numBlocks uint64, k uint32) filterFactory {
	return func() filterOps {
		f := gloom.NewWithParams(numBlocks, k)
		return filterOps{add: f.Add, test: f.Test}
	}
}

func makeFilterFactoryFromTarget(expectedItems uint64, fpRate float64) filterFactory {
	return func() filterOps {
		f := gloom.New(expectedItems, fpRate)
		return filterOps{add: f.Add, test: f.Test}
	}
}

func makeAtomicFilterFactory(expectedItems uint64, fpRate float64) filterFactory {
	return func() filterOps {
		f := gloom.NewAtomic(expectedItems, fpRate)
		return filterOps{add: f.Add, test: f.Test}
	}
}

func makeShardedFilterFactory(expectedItems uint64, fpRate float64) filterFactory {
	return func() filterOps {
		f := gloom.NewShardedAtomicDefault(expectedItems, fpRate)
		return filterOps{add: f.Add, test: f.Test}
	}
}

func runTrials(factory filterFactory, insertCount, testCount, numTrials int) []trialResult {
	results := make([]trialResult, numTrials)

	// Key generation uses binary encoding: 4 bytes trial + 4 bytes index.
	// Each (trial, index) pair produces a unique 8-byte key. This is much
	// faster than fmt.Appendf string formatting (~2ns vs ~50ns per key),
	// which matters enormously at 10M items × 1000 trials = 10B keys.
	//
	// Each trial uses different keys (different trial prefix) so that items
	// hash to different blocks, producing natural per-trial variance in FP
	// rates. Without this, xxh3's determinism would make every trial identical.
	var key [8]byte

	for t := range numTrials {
		ops := factory()

		binary.LittleEndian.PutUint32(key[:4], uint32(t))

		for i := range insertCount {
			binary.LittleEndian.PutUint32(key[4:], uint32(i))
			ops.add(key[:])
		}

		fpCount := 0
		for i := range testCount {
			binary.LittleEndian.PutUint32(key[4:], uint32(insertCount+i))
			if ops.test(key[:]) {
				fpCount++
			}
		}

		results[t] = trialResult{
			fpCount: fpCount,
			fpRate:  float64(fpCount) / float64(testCount),
		}
	}

	return results
}

func standardFP(k uint32, n uint64, numBlocks uint64) float64 {
	kf := float64(k)
	nf := float64(n)
	m := float64(numBlocks) * float64(gloom.BlockBits)
	return math.Pow(1-math.Exp(-kf*nf/m), kf)
}

func runFPVsLoad() error {
	const (
		expectedItems = 10_000
		targetFP      = 0.01
		testKeys      = defaultTestKeys
		numTrials     = defaultTrials
	)

	numBlocks, k, _ := gloom.OptimalParams(expectedItems, targetFP)

	w, err := newCSVWriter("fp-vs-load.csv")
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.WriteHeader(
		"load_factor",
		"observed_fp_mean", "observed_fp_ci_lo", "observed_fp_ci_hi",
		"theoretical_fp",
		"atomic_fp_mean", "sharded_fp_mean",
	); err != nil {
		return err
	}

	// 20 steps from 25% to 250%. Starting at 25% avoids load levels where
	// the FP rate is effectively zero (unmeasurable), which can't render on
	// a log-scale y-axis.
	for step := range 20 {
		loadFactor := 0.25 + float64(step)*(2.25/19.0)
		insertCount := int(float64(expectedItems) * loadFactor)

		progress("fp-vs-load: load=%.0f%% (%d/%d)", loadFactor*100, step+1, 20)

		factory := makeFilterFactory(numBlocks, k)
		trials := runTrials(factory, insertCount, testKeys, numTrials)
		s := summarizeTrials(trials)

		theoretical := gloom.EstimateFalsePositiveRate(numBlocks, k, uint64(insertCount))

		atomicFactory := makeAtomicFilterFactory(expectedItems, targetFP)
		atomicTrials := runTrials(atomicFactory, insertCount, testKeys, 1000)
		atomicS := summarizeTrials(atomicTrials)

		shardedFactory := makeShardedFilterFactory(expectedItems, targetFP)
		shardedTrials := runTrials(shardedFactory, insertCount, testKeys, 1000)
		shardedS := summarizeTrials(shardedTrials)

		if err := w.WriteRow(
			loadFactor,
			s.mean, s.ciLo, s.ciHi,
			theoretical,
			atomicS.mean, shardedS.mean,
		); err != nil {
			return err
		}
	}

	progress("fp-vs-load: done")
	return nil
}

func runFPVsSize() error {
	const targetFP = 0.01

	sizes := []int{
		1_000, 5_000, 10_000, 50_000, 100_000,
		500_000, 1_000_000, 5_000_000, 10_000_000,
	}

	w, err := newCSVWriter("fp-vs-size.csv")
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.WriteHeader(
		"expected_items",
		"observed_fp_mean", "observed_fp_ci_lo", "observed_fp_ci_hi",
		"theoretical_fp", "target_fp",
	); err != nil {
		return err
	}

	for i, size := range sizes {
		testKeys := min(size, 100_000)

		// Scale trial count with filter size. At large sizes, each trial is
		// expensive but also more precise (100K test keys per trial), so fewer
		// trials are needed. 1,000 trials at 100K test keys gives a 95% CI
		// of ±0.06% — well within visual precision.
		numTrials := defaultTrials
		if size > 100_000 {
			numTrials = 1_000
		}

		progress("fp-vs-size: n=%d trials=%d (%d/%d)", size, numTrials, i+1, len(sizes))

		factory := makeFilterFactoryFromTarget(uint64(size), targetFP)
		trials := runTrials(factory, size, testKeys, numTrials)
		s := summarizeTrials(trials)

		numBlocks, k, _ := gloom.OptimalParams(uint64(size), targetFP)
		theoretical := gloom.EstimateFalsePositiveRate(numBlocks, k, uint64(size))

		if err := w.WriteRow(
			size,
			s.mean, s.ciLo, s.ciHi,
			theoretical, targetFP,
		); err != nil {
			return err
		}
	}

	progress("fp-vs-size: done")
	return nil
}

func runObservedVsTheoretical() error {
	const (
		expectedItems = 10_000
		targetFP      = 0.01
		testKeys      = defaultTestKeys
		numTrials     = defaultTrials
	)

	loadFactors := []float64{0.25, 0.50, 0.75, 1.00, 1.50}

	w, err := newCSVWriter("observed-vs-theoretical.csv")
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.WriteHeader("k", "load_factor", "observed_fp_mean", "theoretical_fp"); err != nil {
		return err
	}

	numBlocks, _, _ := gloom.OptimalParams(expectedItems, targetFP)

	total := 12 * len(loadFactors)
	done := 0

	for k := uint32(3); k <= 14; k++ {
		for _, lf := range loadFactors {
			insertCount := int(float64(expectedItems) * lf)
			if insertCount == 0 {
				insertCount = 1
			}
			done++
			progress("observed-vs-theoretical: k=%d load=%.0f%% (%d/%d)", k, lf*100, done, total)

			factory := makeFilterFactory(numBlocks, k)
			trials := runTrials(factory, insertCount, testKeys, numTrials)
			s := summarizeTrials(trials)

			theoretical := gloom.EstimateFalsePositiveRate(numBlocks, k, uint64(insertCount))

			if err := w.WriteRow(k, lf, s.mean, theoretical); err != nil {
				return err
			}
		}
	}

	progress("observed-vs-theoretical: done")
	return nil
}

func runFPVsK() error {
	const (
		expectedItems = 10_000
		targetFP      = 0.01
		insertCount   = expectedItems
		testKeys      = defaultTestKeys
		numTrials     = defaultTrials
	)

	numBlocks, _, _ := gloom.OptimalParams(expectedItems, targetFP)

	w, err := newCSVWriter("fp-vs-k.csv")
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.WriteHeader(
		"k",
		"observed_fp_mean", "observed_fp_ci_lo", "observed_fp_ci_hi",
		"theoretical_fp",
	); err != nil {
		return err
	}

	for k := uint32(3); k <= 14; k++ {
		progress("fp-vs-k: k=%d (%d/12)", k, k-2)

		factory := makeFilterFactory(numBlocks, k)
		trials := runTrials(factory, insertCount, testKeys, numTrials)
		s := summarizeTrials(trials)

		theoretical := gloom.EstimateFalsePositiveRate(numBlocks, k, uint64(insertCount))

		if err := w.WriteRow(k, s.mean, s.ciLo, s.ciHi, theoretical); err != nil {
			return err
		}
	}

	progress("fp-vs-k: done")
	return nil
}

func runFPVsBPK() error {
	const (
		expectedItems = 10_000
		insertCount   = expectedItems
		testKeys      = defaultTestKeys
		numTrials     = defaultTrials
	)

	w, err := newCSVWriter("fp-vs-bpk.csv")
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.WriteHeader(
		"bits_per_key", "k", "num_blocks",
		"observed_fp_mean", "observed_fp_ci_lo", "observed_fp_ci_hi",
		"theoretical_fp", "standard_formula_fp",
	); err != nil {
		return err
	}

	for bpk := 4; bpk <= 20; bpk++ {
		progress("fp-vs-bpk: bpk=%d (%d/17)", bpk, bpk-3)

		totalBits := float64(expectedItems) * float64(bpk)
		numBlocks := uint64(math.Ceil(totalBits / float64(gloom.BlockBits)))
		if numBlocks == 0 {
			numBlocks = 1
		}

		actualBPK := float64(numBlocks*gloom.BlockBits) / float64(expectedItems)
		kFloat := actualBPK * 0.6931471805599453
		k := max(uint32(math.Round(kFloat)), 3)
		k = min(k, 14)

		factory := makeFilterFactory(numBlocks, k)
		trials := runTrials(factory, insertCount, testKeys, numTrials)
		s := summarizeTrials(trials)

		theoretical := gloom.EstimateFalsePositiveRate(numBlocks, k, uint64(insertCount))
		standard := standardFP(k, uint64(insertCount), numBlocks)

		if err := w.WriteRow(
			bpk, k, numBlocks,
			s.mean, s.ciLo, s.ciHi,
			theoretical, standard,
		); err != nil {
			return err
		}
	}

	progress("fp-vs-bpk: done")
	return nil
}

func runFPDistribution() error {
	const (
		expectedItems = 10_000
		targetFP      = 0.01
		insertCount   = expectedItems
		testKeys      = defaultTestKeys
		numTrials     = defaultTrials
	)

	progress("fp-distribution: running %d trials...", numTrials)

	factory := makeFilterFactoryFromTarget(uint64(expectedItems), targetFP)
	trials := runTrials(factory, insertCount, testKeys, numTrials)

	numBlocks, k, _ := gloom.OptimalParams(uint64(expectedItems), targetFP)
	theoreticalP := gloom.EstimateFalsePositiveRate(numBlocks, k, uint64(insertCount))

	w, err := newCSVWriter("fp-distribution.csv")
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.WriteHeader("trial", "fp_count"); err != nil {
		return err
	}

	for i, t := range trials {
		if err := w.WriteRow(i, t.fpCount); err != nil {
			return err
		}
	}

	meta, err := newCSVWriter("fp-distribution-meta.csv")
	if err != nil {
		return err
	}
	defer meta.Close()

	s := summarizeTrials(trials)

	if err := meta.WriteHeader(
		"theoretical_p", "test_keys", "num_trials",
		"observed_mean", "observed_stddev",
	); err != nil {
		return err
	}
	if err := meta.WriteRow(
		theoreticalP, testKeys, numTrials,
		s.mean, s.stdDev,
	); err != nil {
		return err
	}

	progress("fp-distribution: done (theoretical_p=%.6f, observed_mean=%.6f)", theoreticalP, s.mean)
	return nil
}
