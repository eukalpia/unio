package stats

import (
	"fmt"
	"strings"
)

// MarkdownHeader returns the header and alignment lines of the results table
// that MarkdownRow fills. The lines are joined with a newline and have no
// trailing newline.
func MarkdownHeader() string {
	return "| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |\n" +
		"| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"
}

// MarkdownRow formats one table row for s, labelled with the workload and
// operation names. Pipe characters in the labels are escaped. The row has no
// trailing newline.
func MarkdownRow(workload, operation string, s Summary) string {
	return fmt.Sprintf("| %s | %s | %d | %d | %.2f | %.1f | %.2f | %.3f | %.3f | %.3f |",
		escapeCell(workload), escapeCell(operation),
		s.Operations, s.Errors, s.DurationSeconds, s.OpsPerSecond, s.MiBPerSecond,
		s.LatencyMs.P50, s.LatencyMs.P95, s.LatencyMs.P99)
}

// escapeCell escapes pipe characters so a label cannot split a table cell.
func escapeCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}
