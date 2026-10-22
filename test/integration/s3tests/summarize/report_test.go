package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// sampleReport is a JUnit document in the shape pytest writes, with each
// outcome present once and one name that appears twice.
const sampleReport = `<?xml version="1.0" encoding="utf-8"?>
<testsuites>
  <testsuite name="pytest" tests="6">
    <testcase classname="s3tests.functional.test_s3" name="test_pass" time="0.1"/>
    <testcase classname="s3tests.functional.test_s3" name="test_fail" time="0.2">
      <failure message="assert 1 == 2">trace</failure>
    </testcase>
    <testcase classname="s3tests.functional.test_s3" name="test_error" time="0.1">
      <error message="setup">trace</error>
    </testcase>
    <testcase classname="s3tests.functional.test_s3" name="test_skip" time="0.0">
      <skipped message="not applicable" type="pytest.skip">skip</skipped>
    </testcase>
    <testcase classname="s3tests.functional.test_s3" name="test_dup" time="0.0">
      <skipped message="first run" type="pytest.skip">skip</skipped>
    </testcase>
    <testcase classname="s3tests.functional.test_s3" name="test_dup" time="0.0"/>
  </testsuite>
</testsuites>`

func TestLoadSubset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "node IDs reduced to names",
			input: "s3tests/functional/test_s3.py::test_a\ns3tests/functional/test_s3.py::test_b\n",
			want:  []string{"test_a", "test_b"},
		},
		{
			name:  "comments and blank lines ignored",
			input: "# header\n\n   \ns3tests/functional/test_s3.py::test_a  \n",
			want:  []string{"test_a"},
		},
		{
			name:  "duplicates removed in file order",
			input: "x.py::test_b\nx.py::test_a\nx.py::test_b\n",
			want:  []string{"test_b", "test_a"},
		},
		{
			name:  "parameter list removed",
			input: "x.py::test_a[case-1]\n",
			want:  []string{"test_a"},
		},
		{
			name:    "empty subset rejected",
			input:   "# nothing here\n",
			wantErr: true,
		},
		{
			name:    "node ID without a name rejected",
			input:   "s3tests/functional/test_s3.py::\n",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := loadSubset(strings.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("loadSubset() error = nil, want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loadSubset() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("loadSubset() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseReport(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  map[string]Outcome
	}{
		{
			name:  "each outcome, with the worst outcome kept for a repeated name",
			input: sampleReport,
			want: map[string]Outcome{
				"test_pass":  Passed,
				"test_fail":  Failed,
				"test_error": Failed,
				"test_skip":  Skipped,
				"test_dup":   Skipped,
			},
		},
		{
			name:  "bare testsuite root",
			input: `<testsuite><testcase name="test_a"/><testcase name="test_b"><failure/></testcase></testsuite>`,
			want:  map[string]Outcome{"test_a": Passed, "test_b": Failed},
		},
		{
			name:  "empty report",
			input: `<testsuites></testsuites>`,
			want:  map[string]Outcome{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseReport(strings.NewReader(tc.input))
			if err != nil {
				t.Fatalf("parseReport() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("parseReport() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseReportMalformed(t *testing.T) {
	t.Parallel()
	if _, err := parseReport(strings.NewReader("<testsuites><testcase")); err == nil {
		t.Fatal("parseReport() error = nil for truncated XML, want an error")
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()
	subset := []string{"a", "b", "c", "d"}
	outcomes := map[string]Outcome{"a": Passed, "b": Failed, "c": Skipped, "extra": Passed}
	got := summarize(subset, outcomes)
	want := Summary{Total: 4, Passed: 1, Failed: 1, Skipped: 1, NotRun: 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("summarize() mismatch (-want +got):\n%s", diff)
	}
	if got, want := got.SubsetRate(), 0.25; got != want {
		t.Errorf("SubsetRate() = %v, want %v", got, want)
	}
	if got, want := got.ExecutedRate(), 0.5; got != want {
		t.Errorf("ExecutedRate() = %v, want %v", got, want)
	}
}

func TestRatesWithNothingRun(t *testing.T) {
	t.Parallel()
	var s Summary
	if s.SubsetRate() != 0 || s.ExecutedRate() != 0 {
		t.Fatalf("rates for an empty summary = %v, %v, want 0, 0", s.SubsetRate(), s.ExecutedRate())
	}
}

func TestWriteReportVerdict(t *testing.T) {
	t.Parallel()
	s := Summary{Total: 10, Passed: 8, Failed: 2}
	tests := []struct {
		name       string
		target     float64
		wantResult string
	}{
		{name: "meets the target", target: 0.80, wantResult: "result: PASS"},
		{name: "below the target", target: 0.90, wantResult: "result: FAIL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			if err := writeReport(&buf, s, tc.target); err != nil {
				t.Fatalf("writeReport() error = %v", err)
			}
			if !strings.Contains(buf.String(), tc.wantResult) {
				t.Errorf("report does not contain %q:\n%s", tc.wantResult, buf.String())
			}
		})
	}
}

func TestRun(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subsetFile := filepath.Join(dir, "subset.txt")
	resultsFile := filepath.Join(dir, "junit.xml")
	if err := os.WriteFile(subsetFile, []byte("x.py::test_pass\nx.py::test_fail\nx.py::test_missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultsFile, []byte(sampleReport), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "target met", args: []string{"-results", resultsFile, "-subset", subsetFile, "-target", "0.3"}, wantCode: 0},
		{name: "target missed", args: []string{"-results", resultsFile, "-subset", subsetFile, "-target", "0.9"}, wantCode: 1},
		{name: "default target missed", args: []string{"-results", resultsFile, "-subset", subsetFile}, wantCode: 1},
		{name: "missing results flag", args: []string{"-subset", subsetFile}, wantCode: 2},
		{name: "target out of range", args: []string{"-results", resultsFile, "-subset", subsetFile, "-target", "1.5"}, wantCode: 2},
		{name: "unknown flag", args: []string{"-nope"}, wantCode: 2},
		{name: "results file missing", args: []string{"-results", filepath.Join(dir, "absent.xml"), "-subset", subsetFile}, wantCode: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if got := run(tc.args, &stdout, &stderr); got != tc.wantCode {
				t.Fatalf("run() = %d, want %d (stderr: %s)", got, tc.wantCode, stderr.String())
			}
		})
	}
}
