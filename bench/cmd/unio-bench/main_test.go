package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eukalpia/unio/bench/internal/report"
)

const secretValue = "s3cr3t-do-not-print-0123456789"

func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestRunMissingCredentialsIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-endpoint", testEndpoint}, envOf(nil), &stdout, &stderr)
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), accessKeyEnv+" is not set") {
		t.Fatalf("stderr = %q, want it to name %s", stderr.String(), accessKeyEnv)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing when the run never starts", stdout.String())
	}
}

func TestRunHelpExitsZero(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"-h"}, envOf(nil), &stdout, &stderr); code != exitOK {
		t.Fatalf("exit code = %d, want %d", code, exitOK)
	}
}

func TestRunBadFlagIsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := envOf(map[string]string{accessKeyEnv: "ak", secretKeyEnv: secretValue})
	if code := run(context.Background(), []string{"-concurrency=0", "-endpoint", testEndpoint}, env, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestRunNeverPrintsTheSecret(t *testing.T) {
	env := envOf(map[string]string{accessKeyEnv: "bench-access", secretKeyEnv: secretValue})
	tests := []struct {
		name string
		args []string
	}{
		{name: "endpoint with userinfo", args: []string{"-endpoint", "http://u:" + secretValue + "@localhost:9001"}},
		{name: "endpoint with query", args: []string{"-endpoint", "http://localhost:9001/?sig=" + secretValue}},
		{name: "bad scheme", args: []string{"-endpoint", "ftp://" + secretValue}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tc.args, env, &stdout, &stderr)
			if code == exitOK {
				t.Fatal("invalid endpoint was accepted")
			}
			if strings.Contains(stderr.String(), secretValue) || strings.Contains(stdout.String(), secretValue) {
				t.Fatalf("output leaks the secret: stderr=%q stdout=%q", stderr.String(), stdout.String())
			}
		})
	}
}

func TestWriteResultsWritesJSONAndMarkdownFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := config{out: filepath.Join(dir, "out.json"), markdown: filepath.Join(dir, "out.md")}
	rep := report.Report{SchemaVersion: report.SchemaVersion, Tool: report.ToolName}

	var stdout bytes.Buffer
	if err := writeResults(cfg, rep, &stdout); err != nil {
		t.Fatalf("writeResults: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing when -out is set", stdout.String())
	}
	js, err := os.ReadFile(cfg.out)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	if !strings.Contains(string(js), `"schema_version": 1`) {
		t.Fatalf("json file lacks schema_version: %s", js)
	}
	md, err := os.ReadFile(cfg.markdown)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.HasPrefix(string(md), "# unio-bench results") {
		t.Fatalf("markdown file has unexpected header: %s", md)
	}
}

func TestWriteResultsDefaultsToStdout(t *testing.T) {
	var stdout bytes.Buffer
	rep := report.Report{SchemaVersion: report.SchemaVersion, Tool: report.ToolName}
	if err := writeResults(config{}, rep, &stdout); err != nil {
		t.Fatalf("writeResults: %v", err)
	}
	if !strings.HasPrefix(stdout.String(), "{") {
		t.Fatalf("stdout = %q, want JSON", stdout.String())
	}
}
