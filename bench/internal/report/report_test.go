package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/bench/internal/stats"
)

func sampleReport() Report {
	return Report{
		SchemaVersion: SchemaVersion,
		Tool:          ToolName,
		StartedAt:     time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		FinishedAt:    time.Date(2026, 10, 22, 12, 0, 5, 0, time.UTC),
		Environment:   Environment{GoVersion: "go1.27.3", GOOS: "linux", GOARCH: "amd64", NumCPU: 8},
		Config: Config{
			Endpoint:        "http://localhost:9000",
			Region:          "us-east-1",
			PathStyle:       true,
			Bucket:          "unio-bench-test",
			Workloads:       []string{"put-4k"},
			Concurrency:     4,
			DurationSeconds: 3,
			WarmupSeconds:   1,
			ListKeys:        2000,
			ListPage:        1000,
			Seed:            1,
		},
		Workloads: []Workload{{
			Name: "put-4k",
			Operations: []Operation{{
				Operation: "PUT",
				Summary: stats.Summary{
					Operations:      4,
					Errors:          2,
					Bytes:           4194304,
					DurationSeconds: 2,
					OpsPerSecond:    2,
					MiBPerSecond:    2,
					LatencyMs:       stats.Latency{Min: 1, Mean: 2.5, P50: 2, P95: 4, P99: 4, Max: 4},
				},
			}},
		}},
	}
}

func TestWriteJSONGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	const want = `{
  "schema_version": 1,
  "tool": "unio-bench",
  "started_at": "2026-10-22T12:00:00Z",
  "finished_at": "2026-10-22T12:00:05Z",
  "interrupted": false,
  "environment": {
    "go_version": "go1.27.3",
    "goos": "linux",
    "goarch": "amd64",
    "num_cpu": 8
  },
  "config": {
    "endpoint": "http://localhost:9000",
    "region": "us-east-1",
    "path_style": true,
    "bucket": "unio-bench-test",
    "workloads": [
      "put-4k"
    ],
    "concurrency": 4,
    "duration_seconds": 3,
    "warmup_seconds": 1,
    "list_keys": 2000,
    "list_page": 1000,
    "seed": 1
  },
  "workloads": [
    {
      "name": "put-4k",
      "operations": [
        {
          "operation": "PUT",
          "summary": {
            "operations": 4,
            "errors": 2,
            "dropped_samples": 0,
            "bytes": 4194304,
            "duration_seconds": 2,
            "ops_per_second": 2,
            "mib_per_second": 2,
            "latency_ms": {
              "min": 1,
              "mean": 2.5,
              "p50": 2,
              "p95": 4,
              "p99": 4,
              "max": 4
            }
          }
        }
      ]
    }
  ]
}
`
	if got := buf.String(); got != want {
		t.Fatalf("JSON mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestWriteJSONHasNoCredentialFields(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	for _, banned := range []string{"secret", "access_key", "password", "token_value"} {
		if strings.Contains(strings.ToLower(buf.String()), banned) {
			t.Errorf("JSON contains %q", banned)
		}
	}
}

func TestWriteMarkdownGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMarkdown(&buf, sampleReport()); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	const want = `# unio-bench results

- Schema version: 1
- Endpoint: http://localhost:9000
- Bucket: unio-bench-test
- Started: 2026-10-22T12:00:00Z
- Finished: 2026-10-22T12:00:05Z
- Interrupted: false
- Concurrency: 4, duration 3s, warmup 1s, seed 1
- Client: go1.27.3 linux/amd64, 8 CPUs

| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| put-4k | PUT | 4 | 2 | 2.00 | 2.0 | 2.00 | 2.000 | 4.000 | 4.000 |
`
	if got := buf.String(); got != want {
		t.Fatalf("Markdown mismatch\n got:\n%s\nwant:\n%s", got, want)
	}
}
