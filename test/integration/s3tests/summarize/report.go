package main

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Outcome is what the JUnit report says about one test.
type Outcome int

const (
	// NotRun means the test is in the subset but has no entry in the report.
	NotRun Outcome = iota
	// Passed means the test ran and succeeded.
	Passed
	// Skipped means the test ran and skipped itself.
	Skipped
	// Failed means the test ran and failed or errored.
	Failed
)

// severity orders outcomes, so that the worst one wins when a test name
// appears more than once in the report.
func (o Outcome) severity() int {
	switch o {
	case Passed:
		return 1
	case Skipped:
		return 2
	case Failed:
		return 3
	default:
		return 0
	}
}

// Summary counts the outcomes of the subset.
type Summary struct {
	Total   int // distinct tests in the subset
	Passed  int
	Failed  int
	Skipped int
	NotRun  int
}

// ExecutedRate is the pass rate over the tests that ran and did not skip. It is
// 0 when no test ran.
func (s Summary) ExecutedRate() float64 {
	ran := s.Passed + s.Failed
	if ran == 0 {
		return 0
	}
	return float64(s.Passed) / float64(ran)
}

// SubsetRate is the pass rate over the whole subset. Skipped and missing tests
// count as not passed, so this is the figure the v0.1 target applies to.
func (s Summary) SubsetRate() float64 {
	if s.Total == 0 {
		return 0
	}
	return float64(s.Passed) / float64(s.Total)
}

// loadSubset reads a subset file. Each non-blank line that does not start with
// "#" is a pytest node ID. The test name is the part after the last "::", with
// any parameter list removed. It returns the distinct names in file order.
func loadSubset(r io.Reader) ([]string, error) {
	sc := bufio.NewScanner(r)
	seen := make(map[string]bool)
	var names []string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.LastIndex(line, "::"); i >= 0 {
			name = line[i+len("::"):]
		}
		if i := strings.Index(name, "["); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return nil, fmt.Errorf("subset line %q has no test name", line)
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read subset: %w", err)
	}
	if len(names) == 0 {
		return nil, errors.New("subset contains no tests")
	}
	return names, nil
}

// junitCase is the part of a JUnit <testcase> element that the summary uses.
type junitCase struct {
	Name    string    `xml:"name,attr"`
	Failure *struct{} `xml:"failure"`
	Error   *struct{} `xml:"error"`
	Skipped *struct{} `xml:"skipped"`
}

// parseReport reads a JUnit XML report and returns the outcome of each test
// name. Test cases are found at any depth, so both <testsuites> and a bare
// <testsuite> root work. A name that appears more than once keeps its worst
// outcome.
func parseReport(r io.Reader) (map[string]Outcome, error) {
	dec := xml.NewDecoder(r)
	outcomes := make(map[string]Outcome)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse JUnit report: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "testcase" {
			continue
		}
		var tc junitCase
		if err := dec.DecodeElement(&tc, &start); err != nil {
			return nil, fmt.Errorf("parse testcase: %w", err)
		}
		o := Passed
		switch {
		case tc.Failure != nil || tc.Error != nil:
			o = Failed
		case tc.Skipped != nil:
			o = Skipped
		}
		if prev, seen := outcomes[tc.Name]; !seen || o.severity() > prev.severity() {
			outcomes[tc.Name] = o
		}
	}
	return outcomes, nil
}

// summarize counts the outcome of every subset name. A name with no entry in
// outcomes is counted as NotRun.
func summarize(subset []string, outcomes map[string]Outcome) Summary {
	s := Summary{Total: len(subset)}
	for _, name := range subset {
		o, ok := outcomes[name]
		if !ok {
			o = NotRun
		}
		switch o {
		case Passed:
			s.Passed++
		case Failed:
			s.Failed++
		case Skipped:
			s.Skipped++
		default:
			s.NotRun++
		}
	}
	return s
}

// writeReport prints the summary and the verdict against target.
func writeReport(w io.Writer, s Summary, target float64) error {
	verdict := "PASS"
	if s.SubsetRate() < target {
		verdict = "FAIL"
	}
	_, err := fmt.Fprintf(w,
		"ceph/s3-tests v0.1 subset: %d tests\n"+
			"  passed:   %d\n"+
			"  failed:   %d\n"+
			"  skipped:  %d\n"+
			"  not run:  %d\n"+
			"pass rate over the subset:      %5.1f%% (%d/%d), target %.1f%%\n"+
			"pass rate over executed tests:  %5.1f%% (%d/%d)\n"+
			"result: %s\n",
		s.Total, s.Passed, s.Failed, s.Skipped, s.NotRun,
		100*s.SubsetRate(), s.Passed, s.Total, 100*target,
		100*s.ExecutedRate(), s.Passed, s.Passed+s.Failed,
		verdict,
	)
	return err
}
