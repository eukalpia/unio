package engine

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/gc"
)

// Default values of Options fields that are zero.
const (
	DefaultGCQueueSize         = 4096
	DefaultGCWorkers           = 4
	DefaultDeleteConcurrency   = 16
	DefaultMaxConcurrentWrites = 64
	DefaultBufferBudget        = 64 << 20
)

// Options configures an Engine. The zero value is valid and selects the
// defaults.
type Options struct {
	// InlineThreshold is the largest object stored inline in metadata, in
	// bytes. Zero selects object.DefaultInlineThreshold. A negative value
	// disables the inline tier: every object, zero-byte ones included, is
	// erasure-coded and referenced by a part. Values above
	// object.MaxInlineThreshold are rejected.
	InlineThreshold int64
	// Clock supplies modification times. Nil selects time.Now.
	Clock func() time.Time
	// Logger receives operational messages. Object data and keys are never
	// logged. Nil discards messages.
	Logger *slog.Logger
	// GCQueueSize bounds the backlog of data deletions. Zero selects
	// DefaultGCQueueSize.
	GCQueueSize int
	// GCWorkers is the number of concurrent data deletions. Zero selects
	// DefaultGCWorkers.
	GCWorkers int
	// GCTimeout bounds one data deletion. Zero selects gc.DefaultTimeout.
	GCTimeout time.Duration
	// DeleteConcurrency bounds how many keys one DeleteObjects call deletes
	// in parallel. Zero selects DefaultDeleteConcurrency.
	DeleteConcurrency int
	// MaxConcurrentWrites bounds the erasure-coded writes in flight across
	// the engine. Writes wait for a slot or for their context. Zero selects
	// DefaultMaxConcurrentWrites.
	MaxConcurrentWrites int
	// BufferBudget is the memory budget, in bytes, for pooled I/O buffers
	// that the engine creates for itself. It must be at least the inline
	// threshold. Zero selects DefaultBufferBudget. It is ignored when Budget
	// is set.
	BufferBudget int64
	// Budget is a memory budget shared with the caller, for example with the
	// erasure sets and the rest of the node. When it is non-nil, every buffer
	// the engine uses is charged to it instead of to a private budget of
	// BufferBudget bytes. It must be at least the inline threshold.
	Budget *bufpool.Budget
}

// inlineDisabled is the resolved inline threshold when the inline tier is
// off.
const inlineDisabled = -1

// resolved is Options with every default applied and every value checked.
type resolved struct {
	// inlineThreshold is inlineDisabled, or the largest inline size in bytes.
	inlineThreshold     int64
	clock               func() time.Time
	logger              *slog.Logger
	gc                  gc.Config
	deleteConcurrency   int
	maxConcurrentWrites int
	bufferBudget        int64
}

func (o Options) resolve() (resolved, error) {
	var r resolved
	r.inlineThreshold = o.InlineThreshold
	switch {
	case r.inlineThreshold == 0:
		r.inlineThreshold = object.DefaultInlineThreshold
	case r.inlineThreshold < 0:
		r.inlineThreshold = inlineDisabled
	case r.inlineThreshold > object.MaxInlineThreshold:
		return resolved{}, fmt.Errorf("engine: inline threshold %d above the maximum %d", r.inlineThreshold, object.MaxInlineThreshold)
	}
	r.clock = o.Clock
	if r.clock == nil {
		r.clock = time.Now
	}
	r.logger = o.Logger
	if r.logger == nil {
		r.logger = slog.New(slog.DiscardHandler)
	}
	var err error
	if r.gc, err = resolveGC(o); err != nil {
		return resolved{}, err
	}
	r.gc.Logger = r.logger
	r.deleteConcurrency, err = positiveOr(o.DeleteConcurrency, DefaultDeleteConcurrency, "delete concurrency")
	if err != nil {
		return resolved{}, err
	}
	r.maxConcurrentWrites, err = positiveOr(o.MaxConcurrentWrites, DefaultMaxConcurrentWrites, "max concurrent writes")
	if err != nil {
		return resolved{}, err
	}
	r.bufferBudget = o.BufferBudget
	switch {
	case r.bufferBudget == 0:
		r.bufferBudget = DefaultBufferBudget
	case r.bufferBudget < 0:
		return resolved{}, fmt.Errorf("engine: negative buffer budget %d", r.bufferBudget)
	}
	return r, nil
}

func resolveGC(o Options) (gc.Config, error) {
	var cfg gc.Config
	var err error
	if cfg.QueueSize, err = positiveOr(o.GCQueueSize, DefaultGCQueueSize, "gc queue size"); err != nil {
		return gc.Config{}, err
	}
	if cfg.Workers, err = positiveOr(o.GCWorkers, DefaultGCWorkers, "gc workers"); err != nil {
		return gc.Config{}, err
	}
	if o.GCTimeout < 0 {
		return gc.Config{}, fmt.Errorf("engine: negative gc timeout %s", o.GCTimeout)
	}
	cfg.Timeout = o.GCTimeout
	return cfg, nil
}

// positiveOr returns n, or def when n is zero. Negative values are errors.
func positiveOr(n, def int, what string) (int, error) {
	switch {
	case n == 0:
		return def, nil
	case n < 0:
		return 0, fmt.Errorf("engine: %s must not be negative, got %d", what, n)
	}
	return n, nil
}
