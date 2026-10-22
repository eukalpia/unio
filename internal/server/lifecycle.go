package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// stopFunc releases one component. It receives a context bounded by the
// shutdown timeout.
type stopFunc func(ctx context.Context) error

// stopStep is one registered release, named for the log.
type stopStep struct {
	name string
	stop stopFunc
}

// lifecycle records how to release every started component, and runs the
// background workers of the node.
type lifecycle struct {
	logger *slog.Logger

	mu    sync.Mutex
	steps []stopStep
	done  bool

	// bg is cancelled first on shutdown; bgWait tracks the workers using it.
	bg       context.Context
	bgCancel context.CancelFunc
	bgWait   sync.WaitGroup
}

// newLifecycle returns an empty lifecycle.
func newLifecycle(logger *slog.Logger) *lifecycle {
	bg, cancel := context.WithCancel(context.Background())
	return &lifecycle{logger: logger, bg: bg, bgCancel: cancel}
}

// onStop registers stop, to run before every step registered earlier.
func (l *lifecycle) onStop(name string, stop stopFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.steps = append(l.steps, stopStep{name: name, stop: stop})
}

// onClose registers a release that takes no context.
func (l *lifecycle) onClose(name string, closeFn func() error) {
	l.onStop(name, func(context.Context) error { return closeFn() })
}

// goBackground runs fn in a goroutine with the background context. Workers
// must return once that context is cancelled; shutdown waits for them before
// releasing the components they use.
func (l *lifecycle) goBackground(name string, fn func(ctx context.Context)) {
	l.bgWait.Add(1)
	go func() {
		defer l.bgWait.Done()
		fn(l.bg)
		if l.bg.Err() == nil {
			l.logger.Error("background worker stopped unexpectedly", "worker", name)
		}
	}()
}

// stopBackground cancels the background workers and waits for them, or for
// ctx to end.
func (l *lifecycle) stopBackground(ctx context.Context) error {
	l.bgCancel()
	done := make(chan struct{})
	go func() {
		l.bgWait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("background workers did not stop: %w", ctx.Err())
	}
}

// shutdown runs every registered step in reverse order of registration. A
// failing step is logged and does not stop later ones; their errors are
// joined. Only the first call does anything.
func (l *lifecycle) shutdown(ctx context.Context) error {
	l.mu.Lock()
	if l.done {
		l.mu.Unlock()
		return nil
	}
	l.done = true
	steps := l.steps
	l.steps = nil
	l.mu.Unlock()

	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if err := s.stop(ctx); err != nil {
			l.logger.Error("shutdown step failed", "step", s.name, "error", err.Error())
			errs = append(errs, fmt.Errorf("server: stop %s: %w", s.name, err))
		}
	}
	return errors.Join(errs...)
}
