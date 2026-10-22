package group_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/meta/raftstore/group"
)

// jsonLines decodes one JSON object per line written to buf.
func jsonLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// TestHCLoggerLevels checks that every raft level is written at the slog
// level the store uses: trace, debug and info at debug, warnings and errors
// at their own level.
func TestHCLoggerLevels(t *testing.T) {
	tests := []struct {
		name  string
		write func(hclog.Logger)
		level string
	}{
		{"trace", func(l hclog.Logger) { l.Trace("m") }, "DEBUG"},
		{"debug", func(l hclog.Logger) { l.Debug("m") }, "DEBUG"},
		{"info", func(l hclog.Logger) { l.Info("m") }, "DEBUG"},
		{"warn", func(l hclog.Logger) { l.Warn("m") }, "WARN"},
		{"error", func(l hclog.Logger) { l.Error("m") }, "ERROR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			l := group.NewHCLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
			tc.write(l)
			lines := jsonLines(t, &buf)
			if len(lines) != 1 || lines[0]["level"] != tc.level || lines[0]["msg"] != "m" {
				t.Fatalf("got %v, want one %s line with msg m", lines, tc.level)
			}
		})
	}
}

// TestHCLoggerFiltersByHandlerLevel checks that the adapter writes nothing the
// handler's level excludes, and that the Is methods agree with the handler.
func TestHCLoggerFiltersByHandlerLevel(t *testing.T) {
	var buf bytes.Buffer
	l := group.NewHCLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if l.IsInfo() || l.IsDebug() || !l.IsWarn() || !l.IsError() {
		t.Fatal("Is methods disagree with a warning level handler")
	}
	l.Info("hidden")
	l.Warn("shown")
	lines := jsonLines(t, &buf)
	if len(lines) != 1 || lines[0]["msg"] != "shown" {
		t.Fatalf("got %v, want only the warning", lines)
	}
}

// TestHCLoggerKeepsAttributes checks that the attributes of With and of the
// call reach the handler, with integers and strings kept as they are.
func TestHCLoggerKeepsAttributes(t *testing.T) {
	var buf bytes.Buffer
	base := group.NewHCLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	l := base.With("component", "raft", "partition", 3)
	l.Debug("election", "term", 7, "addr", "n1:7000")
	lines := jsonLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	rec := lines[0]
	if rec["component"] != "raft" || rec["partition"] != float64(3) || rec["term"] != float64(7) || rec["addr"] != "n1:7000" {
		t.Fatalf("attributes not kept: %v", rec)
	}
}

// TestHCLoggerRedactsObjectData checks that log entries and byte slices are
// logged as summaries. Raft writes a command with %#v when it receives an
// unexpected RPC, and inline object data sits in the entries of such commands.
func TestHCLoggerRedactsObjectData(t *testing.T) {
	const secret = "inline-object-secret"
	var buf bytes.Buffer
	l := group.NewHCLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cmd := &raft.AppendEntriesRequest{
		Term:         4,
		PrevLogEntry: 9,
		Entries:      []*raft.Log{{Index: 10, Term: 4, Data: []byte(secret)}},
	}
	l.Error("expected heartbeat, got", "command", hclog.Fmt("%#v", cmd))
	l.Debug("by value", "command", hclog.Fmt("%#v", *cmd))
	l.Debug("log pointer", "log", &raft.Log{Index: 11, Term: 4, Data: []byte(secret)})
	l.Debug("log value", "log", raft.Log{Index: 12, Term: 4, Data: []byte(secret)})
	l.Debug("bytes", "data", []byte(secret))

	out := buf.String()
	for _, leaked := range []string{secret, fmt.Sprint([]byte(secret))} {
		if strings.Contains(out, leaked) {
			t.Fatalf("log output contains object data %q:\n%s", leaked, out)
		}
	}
	for _, want := range []string{"entries=1", "index=11", fmt.Sprintf("%d bytes", len(secret))} {
		if !strings.Contains(out, want) {
			t.Fatalf("log output lacks summary %q:\n%s", want, out)
		}
	}
}

// TestHCLoggerNilDiscards checks that a nil slog logger discards every line
// without a panic.
func TestHCLoggerNilDiscards(t *testing.T) {
	l := group.NewHCLogger(nil)
	l.Error("dropped", "key", "value")
	if l.IsError() {
		t.Fatal("a discarding logger reports that errors are written")
	}
}
