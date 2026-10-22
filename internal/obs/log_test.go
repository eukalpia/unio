package obs

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		want    slog.Level
		wantErr bool
	}{
		{name: "debug", in: "debug", want: slog.LevelDebug},
		{name: "info upper case", in: "INFO", want: slog.LevelInfo},
		{name: "warn mixed case", in: "Warn", want: slog.LevelWarn},
		{name: "error", in: "error", want: slog.LevelError},
		{name: "empty", in: "", wantErr: true},
		{name: "unknown", in: "trace", wantErr: true},
		{name: "surrounding space", in: " info", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseLevel(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseLevel(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("ParseLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewLoggerErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		format   string
		nilWrite bool
	}{
		{name: "unknown format", format: "xml"},
		{name: "empty format", format: ""},
		{name: "upper case format", format: "JSON"},
		{name: "nil writer", format: "json", nilWrite: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var w io.Writer
			if !tc.nilWrite {
				w = &bytes.Buffer{}
			}
			logger, err := NewLogger(w, tc.format, slog.LevelInfo)
			if err == nil {
				t.Fatalf("NewLogger(%q) error = nil, want error", tc.format)
			}
			if logger != nil {
				t.Fatalf("NewLogger(%q) logger = %v, want nil on error", tc.format, logger)
			}
		})
	}
}

func TestNewLoggerOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		format    string
		level     slog.Level
		emit      func(*slog.Logger)
		wantIn    []string
		wantNotIn []string
	}{
		{
			name:   "json carries message and attributes",
			format: "json",
			level:  slog.LevelInfo,
			emit:   func(l *slog.Logger) { l.Info("hello", "bucket", "photos") },
			wantIn: []string{`"msg":"hello"`, `"bucket":"photos"`},
		},
		{
			name:   "text carries message and attributes",
			format: "text",
			level:  slog.LevelInfo,
			emit:   func(l *slog.Logger) { l.Info("hello", "bucket", "photos") },
			wantIn: []string{"msg=hello", "bucket=photos"},
		},
		{
			name:      "debug suppressed at info level",
			format:    "json",
			level:     slog.LevelInfo,
			emit:      func(l *slog.Logger) { l.Debug("quiet") },
			wantNotIn: []string{"quiet"},
		},
		{
			name:   "json redacts sensitive attribute",
			format: "json",
			level:  slog.LevelInfo,
			emit: func(l *slog.Logger) {
				l.Info("req", slog.String("secret_key", "hunter2"), slog.String("bucket", "photos"))
			},
			wantIn:    []string{`"secret_key":"` + Redacted + `"`, `"bucket":"photos"`},
			wantNotIn: []string{"hunter2"},
		},
		{
			name:   "text redacts sensitive attribute",
			format: "text",
			level:  slog.LevelInfo,
			emit: func(l *slog.Logger) {
				l.Info("req", slog.String("secret_key", "hunter2"), slog.String("bucket", "photos"))
			},
			wantIn:    []string{"secret_key=" + Redacted, "bucket=photos"},
			wantNotIn: []string{"hunter2"},
		},
		{
			name:   "redacts inside a group",
			format: "json",
			level:  slog.LevelInfo,
			emit: func(l *slog.Logger) {
				l.Info("req", slog.Group("auth", slog.String("token", "abc123"), slog.String("user", "alice")))
			},
			wantIn:    []string{`"token":"` + Redacted + `"`, `"user":"alice"`},
			wantNotIn: []string{"abc123"},
		},
		{
			name:   "redacts every value under a sensitive group",
			format: "json",
			level:  slog.LevelInfo,
			emit: func(l *slog.Logger) {
				l.Info("req", slog.Group("credentials", slog.String("id", "AKIAEXAMPLE")))
			},
			wantIn:    []string{`"credentials":{"id":"` + Redacted + `"}`},
			wantNotIn: []string{"AKIAEXAMPLE"},
		},
		{
			name:   "redacts values under a sensitive WithGroup",
			format: "json",
			level:  slog.LevelInfo,
			emit: func(l *slog.Logger) {
				l.WithGroup("cookie").Info("req", "session", "abc-session-value")
			},
			wantIn:    []string{`"cookie":{"session":"` + Redacted + `"}`},
			wantNotIn: []string{"abc-session-value"},
		},
		{
			name:   "redacts http.Header values by key",
			format: "json",
			level:  slog.LevelInfo,
			emit: func(l *slog.Logger) {
				h := http.Header{"Authorization": {"Bearer s3cr3t"}, "Content-Type": {"application/xml"}}
				l.Info("headers", slog.Any("headers", h))
			},
			wantIn:    []string{Redacted, "application/xml"},
			wantNotIn: []string{"s3cr3t"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger, err := NewLogger(&buf, tc.format, tc.level)
			if err != nil {
				t.Fatalf("NewLogger: %v", err)
			}
			tc.emit(logger)
			out := buf.String()
			for _, want := range tc.wantIn {
				if !strings.Contains(out, want) {
					t.Errorf("output %q does not contain %q", out, want)
				}
			}
			for _, bad := range tc.wantNotIn {
				if strings.Contains(out, bad) {
					t.Errorf("output %q leaks %q", out, bad)
				}
			}
			if tc.format == "json" && out != "" {
				var m map[string]any
				if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &m); err != nil {
					t.Errorf("json output is not a valid object: %v: %q", err, out)
				}
			}
		})
	}
}
