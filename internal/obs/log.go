package obs

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel parses "debug", "info", "warn" or "error" (case-insensitive).
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("obs: unknown log level %q", s)
}

// NewLogger returns a structured logger writing to w. format is "json" or
// "text". Every attribute whose key IsSensitiveKey is replaced by Redacted,
// so a secret logged by mistake does not reach the output.
func NewLogger(w io.Writer, format string, level slog.Level) (*slog.Logger, error) {
	if w == nil {
		return nil, errors.New("obs: logger requires a writer")
	}
	opts := &slog.HandlerOptions{Level: level, ReplaceAttr: replaceAttr}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(w, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(w, opts)), nil
	}
	return nil, fmt.Errorf("obs: unknown log format %q", format)
}
