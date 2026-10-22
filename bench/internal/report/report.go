// Package report defines the document written by unio-bench and renders it
// as JSON or as a Markdown table. The schema holds no credentials: the
// configuration types have no fields for them.
package report

import (
	"time"

	"github.com/eukalpia/unio/bench/internal/stats"
)

// SchemaVersion is the version of the JSON document. It is incremented when a
// field is removed or changes meaning; new optional fields do not bump it.
const SchemaVersion = 1

// ToolName identifies the producer of the document.
const ToolName = "unio-bench"

// Report is the complete result of one unio-bench run.
type Report struct {
	SchemaVersion int         `json:"schema_version"`
	Tool          string      `json:"tool"`
	StartedAt     time.Time   `json:"started_at"`
	FinishedAt    time.Time   `json:"finished_at"`
	Interrupted   bool        `json:"interrupted"`
	Environment   Environment `json:"environment"`
	Config        Config      `json:"config"`
	Workloads     []Workload  `json:"workloads"`
}

// Environment records the client side of the run so results can be compared
// with the same hardware and toolchain. It does not describe the server.
type Environment struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	NumCPU    int    `json:"num_cpu"`
}

// Config is the run configuration. The endpoint is recorded as given, and the
// command line rejects endpoints that carry credentials, a query or a fragment.
type Config struct {
	Endpoint        string   `json:"endpoint"`
	Region          string   `json:"region"`
	PathStyle       bool     `json:"path_style"`
	Bucket          string   `json:"bucket"`
	Workloads       []string `json:"workloads"`
	Concurrency     int      `json:"concurrency"`
	DurationSeconds float64  `json:"duration_seconds"`
	WarmupSeconds   float64  `json:"warmup_seconds"`
	ListKeys        int      `json:"list_keys"`
	ListPage        int      `json:"list_page"`
	Seed            uint64   `json:"seed"`
}

// Workload is the result of one workload.
type Workload struct {
	Name       string      `json:"name"`
	Operations []Operation `json:"operations"`
}

// Operation is the summary of one operation type within a workload.
type Operation struct {
	Operation string        `json:"operation"`
	Summary   stats.Summary `json:"summary"`
}
