package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

type experiment struct {
	name string
	run  func() error
}

var experiments = []experiment{
	{"fp-vs-load", runFPVsLoad},
	{"fp-vs-size", runFPVsSize},
	{"observed-vs-theoretical", runObservedVsTheoretical},
	{"fp-vs-k", runFPVsK},
	{"fp-vs-bpk", runFPVsBPK},
	{"fp-distribution", runFPDistribution},
}

func main() {
	all := flag.Bool("all", false, "run all experiments")
	chart := flag.String("chart", "", "run a single experiment by name")
	list := flag.Bool("list", false, "list available experiments")
	flag.Parse()

	if *list {
		for _, e := range experiments {
			fmt.Println(e.name)
		}
		return
	}

	if !*all && *chart == "" {
		fmt.Fprintln(os.Stderr, "usage: analysis -all | -chart=<name> | -list")
		os.Exit(1)
	}

	var toRun []experiment
	if *all {
		toRun = experiments
	} else {
		found := false
		for _, e := range experiments {
			if e.name == *chart {
				toRun = append(toRun, e)
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "unknown chart: %q\nrun with -list to see available experiments\n", *chart)
			os.Exit(1)
		}
	}

	totalStart := time.Now()
	for _, e := range toRun {
		start := time.Now()
		progress("=== %s ===", e.name)
		if err := e.run(); err != nil {
			fmt.Fprintf(os.Stderr, "error running %s: %v\n", e.name, err)
			os.Exit(1)
		}
		progress("=== %s completed in %s ===\n", e.name, time.Since(start).Round(time.Second))
	}
	progress("all experiments completed in %s", time.Since(totalStart).Round(time.Second))
}
