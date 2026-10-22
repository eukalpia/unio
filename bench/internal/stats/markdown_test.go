package stats

import (
	"testing"
	"time"
)

func TestMarkdownHeaderGolden(t *testing.T) {
	const want = "| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |\n" +
		"| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"
	if got := MarkdownHeader(); got != want {
		t.Fatalf("MarkdownHeader() mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestMarkdownRowGolden(t *testing.T) {
	s := Summarize(Collected{Samples: ms(4, 1, 3, 2), Operations: 4, Errors: 2, Bytes: 4 * mib}, 2*time.Second)
	const want = "| put-4k | PUT | 4 | 2 | 2.00 | 2.0 | 2.00 | 2.000 | 4.000 | 4.000 |"
	if got := MarkdownRow("put-4k", "PUT", s); got != want {
		t.Fatalf("MarkdownRow() mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestMarkdownRowEscapesPipes(t *testing.T) {
	const want = `| a\|b | c\|d | 0 | 0 | 0.00 | 0.0 | 0.00 | 0.000 | 0.000 | 0.000 |`
	if got := MarkdownRow("a|b", "c|d", Summary{}); got != want {
		t.Fatalf("MarkdownRow() = %q, want %q", got, want)
	}
}
