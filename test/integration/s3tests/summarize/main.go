// Command summarize reads the JUnit report written by the ceph/s3-tests runner
// and reports the pass rate over the v0.1 subset. It exits 0 when the subset
// pass rate reaches -target, 1 when it does not, and 2 on a usage or input
// error.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes the command with args and returns the process exit status.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("summarize", flag.ContinueOnError)
	fs.SetOutput(stderr)
	results := fs.String("results", "", "path to the JUnit XML report")
	subsetPath := fs.String("subset", "", "path to subset.txt")
	target := fs.Float64("target", 0.90, "minimum pass rate over the subset, from 0 to 1")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *results == "" || *subsetPath == "" {
		fmt.Fprintln(stderr, "summarize: -results and -subset are required")
		return 2
	}
	if *target < 0 || *target > 1 {
		fmt.Fprintln(stderr, "summarize: -target must be between 0 and 1")
		return 2
	}

	subset, err := readFile(*subsetPath, loadSubset)
	if err != nil {
		fmt.Fprintf(stderr, "summarize: %v\n", err)
		return 2
	}
	outcomes, err := readFile(*results, parseReport)
	if err != nil {
		fmt.Fprintf(stderr, "summarize: %v\n", err)
		return 2
	}

	s := summarize(subset, outcomes)
	if err := writeReport(stdout, s, *target); err != nil {
		fmt.Fprintf(stderr, "summarize: write report: %v\n", err)
		return 2
	}
	if s.SubsetRate() < *target {
		return 1
	}
	return 0
}

// readFile opens path and passes it to parse. Errors name the file.
func readFile[T any](path string, parse func(io.Reader) (T, error)) (T, error) {
	var zero T
	// #nosec G304 -- path is a result file named on the command line; it is cleaned and only read
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return zero, err
	}
	defer f.Close()
	v, err := parse(f)
	if err != nil {
		return zero, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}
