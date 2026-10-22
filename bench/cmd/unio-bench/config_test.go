package main

import (
	"bytes"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/bench/internal/workload"
)

const testEndpoint = "http://localhost:9001"

func TestParseConfigDefaults(t *testing.T) {
	var stderr bytes.Buffer
	cfg, err := parseConfig([]string{"-endpoint", testEndpoint}, &stderr)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	if cfg.endpoint != testEndpoint || cfg.region != "us-east-1" || !cfg.pathStyle {
		t.Errorf("endpoint/region/path-style = %q/%q/%t", cfg.endpoint, cfg.region, cfg.pathStyle)
	}
	if cfg.concurrency != 8 || cfg.duration != 20*time.Second || cfg.warmup != 5*time.Second {
		t.Errorf("concurrency/duration/warmup = %d/%v/%v", cfg.concurrency, cfg.duration, cfg.warmup)
	}
	if cfg.listKeys != 10000 || cfg.listPage != 1000 || cfg.seed != 1 {
		t.Errorf("list-keys/list-page/seed = %d/%d/%d", cfg.listKeys, cfg.listPage, cfg.seed)
	}
	if len(cfg.workloads) != len(workload.Names()) {
		t.Errorf("default workloads = %v, want all", cfg.workloads)
	}
	if !strings.HasPrefix(cfg.bucket, "unio-bench-") || len(cfg.bucket) != len("unio-bench-")+8 {
		t.Errorf("default bucket = %q, want unio-bench-<8 hex>", cfg.bucket)
	}
}

func TestParseConfigValidation(t *testing.T) {
	base := []string{"-endpoint", testEndpoint}
	with := func(extra ...string) []string { return append(append([]string(nil), base...), extra...) }
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{name: "concurrency lower bound", args: with("-concurrency=1")},
		{name: "concurrency upper bound", args: with("-concurrency=512")},
		{name: "concurrency zero", args: with("-concurrency=0"), wantErr: true},
		{name: "concurrency above bound", args: with("-concurrency=513"), wantErr: true},
		{name: "duration one second", args: with("-duration=1s")},
		{name: "duration below one second", args: with("-duration=999ms"), wantErr: true},
		{name: "warmup zero", args: with("-warmup=0")},
		{name: "negative warmup", args: with("-warmup=-1s"), wantErr: true},
		{name: "list keys ceiling", args: with("-list-keys=10000000")},
		{name: "list keys above ceiling", args: with("-list-keys=10000001"), wantErr: true},
		{name: "list keys zero", args: with("-list-keys=0"), wantErr: true},
		{name: "list page upper bound", args: with("-list-page=1000")},
		{name: "list page above bound", args: with("-list-page=1001"), wantErr: true},
		{name: "list page zero", args: with("-list-page=0"), wantErr: true},
		{name: "unknown workload", args: with("-workload=put-4k,bogus"), wantErr: true},
		{name: "all workloads", args: with("-workload=all")},
		{name: "endpoint missing", args: []string{}, wantErr: true},
		{name: "endpoint without scheme", args: []string{"-endpoint", "localhost:9001"}, wantErr: true},
		{name: "endpoint with ftp scheme", args: []string{"-endpoint", "ftp://localhost:21"}, wantErr: true},
		{name: "endpoint without host", args: []string{"-endpoint", "http://"}, wantErr: true},
		{name: "endpoint with userinfo", args: []string{"-endpoint", "http://user:pw@localhost:9001"}, wantErr: true},
		{name: "endpoint with query", args: []string{"-endpoint", "http://localhost:9001/?X-Amz-Signature=abc"}, wantErr: true},
		{name: "endpoint with fragment", args: []string{"-endpoint", "http://localhost:9001/#frag"}, wantErr: true},
		{name: "https endpoint", args: []string{"-endpoint", "https://storage.example.test"}},
		{name: "bucket valid", args: with("-bucket=unio-ok")},
		{name: "bucket too short", args: with("-bucket=ab"), wantErr: true},
		{name: "bucket uppercase", args: with("-bucket=Unio"), wantErr: true},
		{name: "bucket underscore", args: with("-bucket=uni_o"), wantErr: true},
		{name: "empty region", args: with("-region="), wantErr: true},
		{name: "positional argument", args: append(with(), "extra"), wantErr: true},
		{name: "unknown flag", args: with("-nope=1"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			_, err := parseConfig(tc.args, &stderr)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseConfig(%v) error = %v, wantErr %v", tc.args, err, tc.wantErr)
			}
		})
	}
}

func TestParseConfigHelpIsNotAnError(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseConfig([]string{"-h"}, &stderr)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}

func TestParseConfigRejectsCredentialFlags(t *testing.T) {
	var stderr bytes.Buffer
	for _, flagName := range []string{"-access-key", "-secret-key"} {
		_, err := parseConfig([]string{"-endpoint", testEndpoint, flagName, "value"}, &stderr)
		if err == nil {
			t.Errorf("%s was accepted; credentials must come from the environment only", flagName)
		}
	}
}

func TestReadCredentials(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{name: "both set", vars: map[string]string{accessKeyEnv: "ak", secretKeyEnv: "sk"}},
		{name: "access key missing", vars: map[string]string{secretKeyEnv: "sk"}, wantErr: accessKeyEnv},
		{name: "secret key missing", vars: map[string]string{accessKeyEnv: "ak"}, wantErr: secretKeyEnv},
		{name: "none set", vars: map[string]string{}, wantErr: accessKeyEnv},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			creds, err := readCredentials(env(tc.vars))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("readCredentials: %v", err)
				}
				if creds.AccessKey != "ak" || creds.SecretKey != "sk" {
					t.Fatalf("credentials not read from the environment")
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to name %s", err, tc.wantErr)
			}
		})
	}
}

func TestDefaultBucketIsUnique(t *testing.T) {
	a, err := defaultBucket()
	if err != nil {
		t.Fatalf("defaultBucket: %v", err)
	}
	b, err := defaultBucket()
	if err != nil {
		t.Fatalf("defaultBucket: %v", err)
	}
	if a == b {
		t.Fatalf("two default buckets are identical: %q", a)
	}
	if err := validateBucket(a); err != nil {
		t.Fatalf("default bucket %q is invalid: %v", a, err)
	}
}
