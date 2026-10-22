package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/eukalpia/unio/bench/internal/stats"
)

// WriteJSON writes r as indented JSON followed by a newline.
func WriteJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	return nil
}

// WriteMarkdown writes r as a Markdown document: a short configuration list
// followed by one table row per workload operation.
func WriteMarkdown(w io.Writer, r Report) error {
	_, err := fmt.Fprintf(w, "# unio-bench results\n\n"+
		"- Schema version: %d\n"+
		"- Endpoint: %s\n"+
		"- Bucket: %s\n"+
		"- Started: %s\n"+
		"- Finished: %s\n"+
		"- Interrupted: %t\n"+
		"- Concurrency: %d, duration %gs, warmup %gs, seed %d\n"+
		"- Client: %s %s/%s, %d CPUs\n\n",
		r.SchemaVersion, r.Config.Endpoint, r.Config.Bucket,
		r.StartedAt.Format("2006-01-02T15:04:05Z07:00"),
		r.FinishedAt.Format("2006-01-02T15:04:05Z07:00"),
		r.Interrupted, r.Config.Concurrency, r.Config.DurationSeconds,
		r.Config.WarmupSeconds, r.Config.Seed,
		r.Environment.GoVersion, r.Environment.GOOS, r.Environment.GOARCH, r.Environment.NumCPU)
	if err != nil {
		return fmt.Errorf("write markdown header: %w", err)
	}
	if _, err := fmt.Fprintln(w, stats.MarkdownHeader()); err != nil {
		return fmt.Errorf("write markdown table: %w", err)
	}
	for _, wl := range r.Workloads {
		for _, op := range wl.Operations {
			if _, err := fmt.Fprintln(w, stats.MarkdownRow(wl.Name, op.Operation, op.Summary)); err != nil {
				return fmt.Errorf("write markdown row: %w", err)
			}
		}
	}
	return nil
}
