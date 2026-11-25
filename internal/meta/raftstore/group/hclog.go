package group

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
)

// NewHCLogger returns an hclog.Logger that writes to l. Raft and its file
// snapshot store take an hclog.Logger, so the store passes them this adapter.
// A nil l discards every line.
//
// Raft's trace and info lines describe every heartbeat-driven step, election
// and snapshot, so they are written at debug level. Warnings and errors keep
// their level. Values are reduced before they reach l: object data and log
// entries are replaced by a short summary, so no line can carry them.
func NewHCLogger(l *slog.Logger) hclog.Logger {
	if l == nil {
		l = slog.New(slog.DiscardHandler)
	}
	return &hclogAdapter{l: l}
}

// hclogAdapter implements hclog.Logger on top of a slog.Logger. The slog
// handler decides which levels are written, so SetLevel does nothing.
type hclogAdapter struct {
	l    *slog.Logger
	name string
}

// Compile-time interface check.
var _ hclog.Logger = (*hclogAdapter)(nil)

// slogLevel maps a raft level onto the slog level it is written at.
func slogLevel(level hclog.Level) slog.Level {
	switch level {
	case hclog.Warn:
		return slog.LevelWarn
	case hclog.Error:
		return slog.LevelError
	default:
		return slog.LevelDebug
	}
}

// enabled reports whether a line at level is written.
func (a *hclogAdapter) enabled(level hclog.Level) bool {
	return a.l.Enabled(context.Background(), slogLevel(level))
}

// Log writes msg with args at the slog level that corresponds to level.
func (a *hclogAdapter) Log(level hclog.Level, msg string, args ...interface{}) {
	if !a.enabled(level) {
		return
	}
	a.l.Log(context.Background(), slogLevel(level), msg, attrArgs(args)...)
}

// Trace writes msg at debug level.
func (a *hclogAdapter) Trace(msg string, args ...interface{}) { a.Log(hclog.Trace, msg, args...) }

// Debug writes msg at debug level.
func (a *hclogAdapter) Debug(msg string, args ...interface{}) { a.Log(hclog.Debug, msg, args...) }

// Info writes msg at debug level, because raft's info lines are verbose.
func (a *hclogAdapter) Info(msg string, args ...interface{}) { a.Log(hclog.Info, msg, args...) }

// Warn writes msg at warning level.
func (a *hclogAdapter) Warn(msg string, args ...interface{}) { a.Log(hclog.Warn, msg, args...) }

// Error writes msg at error level.
func (a *hclogAdapter) Error(msg string, args ...interface{}) { a.Log(hclog.Error, msg, args...) }

// IsTrace reports whether trace lines are written.
func (a *hclogAdapter) IsTrace() bool { return a.enabled(hclog.Trace) }

// IsDebug reports whether debug lines are written.
func (a *hclogAdapter) IsDebug() bool { return a.enabled(hclog.Debug) }

// IsInfo reports whether info lines are written.
func (a *hclogAdapter) IsInfo() bool { return a.enabled(hclog.Info) }

// IsWarn reports whether warnings are written.
func (a *hclogAdapter) IsWarn() bool { return a.enabled(hclog.Warn) }

// IsError reports whether errors are written.
func (a *hclogAdapter) IsError() bool { return a.enabled(hclog.Error) }

// ImpliedArgs returns nil. The implied arguments are already attributes of
// the slog logger.
func (a *hclogAdapter) ImpliedArgs() []interface{} { return nil }

// With returns a logger that adds args to every line.
func (a *hclogAdapter) With(args ...interface{}) hclog.Logger {
	return &hclogAdapter{l: a.l.With(attrArgs(args)...), name: a.name}
}

// Name returns the name set by Named or ResetNamed.
func (a *hclogAdapter) Name() string { return a.name }

// Named returns a logger whose lines carry the name as a logger attribute.
// Nested names are not joined.
func (a *hclogAdapter) Named(name string) hclog.Logger {
	return a.ResetNamed(name)
}

// ResetNamed returns a logger whose lines carry name as a logger attribute.
func (a *hclogAdapter) ResetNamed(name string) hclog.Logger {
	return &hclogAdapter{l: a.l.With("logger", name), name: name}
}

// SetLevel does nothing: the slog handler decides which levels are written.
func (a *hclogAdapter) SetLevel(hclog.Level) {}

// GetLevel returns the least severe level that is written.
func (a *hclogAdapter) GetLevel() hclog.Level {
	switch {
	case a.enabled(hclog.Debug):
		return hclog.Debug
	case a.enabled(hclog.Warn):
		return hclog.Warn
	case a.enabled(hclog.Error):
		return hclog.Error
	default:
		return hclog.Off
	}
}

// StandardLogger returns a standard library logger that writes at debug
// level. Raft does not use it.
func (a *hclogAdapter) StandardLogger(*hclog.StandardLoggerOptions) *log.Logger {
	return slog.NewLogLogger(a.l.Handler(), slog.LevelDebug)
}

// StandardWriter returns an io.Writer whose writes become debug lines. Raft
// does not use it.
func (a *hclogAdapter) StandardWriter(*hclog.StandardLoggerOptions) io.Writer {
	return stdWriter{a}
}

// stdWriter forwards each write as one debug line.
type stdWriter struct{ a *hclogAdapter }

// Write logs p without its trailing newline.
func (w stdWriter) Write(p []byte) (int, error) {
	w.a.Log(hclog.Debug, strings.TrimRight(string(p), "\r\n"))
	return len(p), nil
}

// attrArgs converts hclog's alternating key and value arguments into slog
// arguments. Every value passes through safeValue.
func attrArgs(args []interface{}) []interface{} {
	out := make([]interface{}, 0, len(args))
	for i := 0; i < len(args); i += 2 {
		if i+1 == len(args) {
			out = append(out, slog.String("!BADKEY", fmt.Sprint(args[i])))
			break
		}
		out = append(out, fmt.Sprint(args[i]), safeValue(args[i+1]))
	}
	return out
}

// safeValue reduces v to a scalar or a short summary that can be logged.
// Raft's log entries carry the bytes of client writes, which include inline
// object data, so entries and byte slices are never written out. The other
// values Raft logs are metadata: terms, indices, addresses, sizes and
// durations.
func safeValue(v interface{}) interface{} {
	switch x := v.(type) {
	case nil:
		return nil
	case hclog.Format:
		if len(x) == 0 {
			return ""
		}
		format, _ := x[0].(string)
		args := make([]interface{}, 0, len(x)-1)
		for _, a := range x[1:] {
			args = append(args, safeValue(a))
		}
		return fmt.Sprintf(format, args...)
	case error:
		return x.Error()
	case time.Duration:
		return x
	case *raft.AppendEntriesRequest:
		if x == nil {
			return nil
		}
		return summarizeAppend(x)
	case raft.AppendEntriesRequest:
		return summarizeAppend(&x)
	case *raft.Log:
		if x == nil {
			return nil
		}
		return summarizeLog(x)
	case raft.Log:
		return summarizeLog(&x)
	case []*raft.Log:
		return fmt.Sprintf("%d log entries", len(x))
	case []byte:
		return fmt.Sprintf("%d bytes", len(x))
	case fmt.Stringer:
		return x.String()
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool:
		return rv.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return rv.Uint()
	case reflect.Float32, reflect.Float64:
		return rv.Float()
	case reflect.String:
		return rv.String()
	}
	return fmt.Sprintf("%v", v)
}

// summarizeAppend describes an AppendEntries request by its term, its
// previous log position, its commit index and its entry count.
func summarizeAppend(req *raft.AppendEntriesRequest) string {
	return fmt.Sprintf("append-entries term=%d prev-index=%d prev-term=%d commit=%d entries=%d",
		req.Term, req.PrevLogEntry, req.PrevLogTerm, req.LeaderCommitIndex, len(req.Entries))
}

// summarizeLog describes a log entry by its position and the size of its data.
func summarizeLog(l *raft.Log) string {
	return fmt.Sprintf("log index=%d term=%d type=%s data=%d bytes", l.Index, l.Term, l.Type, len(l.Data))
}
