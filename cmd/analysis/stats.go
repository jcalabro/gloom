package main

import "math"

type trialResult struct {
	fpCount int
	fpRate  float64
}

type summary struct {
	mean   float64
	ciLo   float64
	ciHi   float64
	stdDev float64
}

func summarizeTrials(trials []trialResult) summary {
	n := float64(len(trials))
	if n == 0 {
		return summary{}
	}

	var sum float64
	for _, t := range trials {
		sum += t.fpRate
	}
	mean := sum / n

	// With fewer than 2 trials, variance is undefined.
	if n < 2 {
		return summary{mean: mean}
	}

	var sumSq float64
	for _, t := range trials {
		d := t.fpRate - mean
		sumSq += d * d
	}
	variance := sumSq / (n - 1)
	sd := math.Sqrt(variance)

	tCrit := 1.96
	if n < 1000 {
		tCrit = 2.0
	}
	margin := tCrit * sd / math.Sqrt(n)

	return summary{
		mean:   mean,
		ciLo:   math.Max(0, mean-margin),
		ciHi:   mean + margin,
		stdDev: sd,
	}
}
