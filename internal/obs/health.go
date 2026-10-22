package obs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"
)

// defaultCheckTimeout bounds one readiness pass of ReadyHandler. The checks
// run concurrently, so this is also the longest a readiness probe waits.
const defaultCheckTimeout = 2 * time.Second

// Health serves liveness and readiness. Checks are registered with
// AddReadinessCheck, and Check runs them.
type Health struct {
	logger       *slog.Logger
	checkTimeout time.Duration

	mu     sync.RWMutex
	checks []readinessCheck
}

// readinessCheck is one named readiness probe.
type readinessCheck struct {
	name string
	fn   func(ctx context.Context) error
}

// checkResult is the outcome of one check, tagged with its registration index.
type checkResult struct {
	index int
	err   error
}

// checkError names one failed readiness check. Check joins these, and
// ReadyHandler reads the names back out of the joined error.
type checkError struct {
	name string
	err  error
}

// Error reports the failed check by name, followed by its cause.
func (e *checkError) Error() string {
	return fmt.Sprintf("readiness check %q failed: %v", e.name, e.err)
}

// Unwrap returns the cause of the failure.
func (e *checkError) Unwrap() error { return e.err }

// healthBody is the JSON document served by both handlers.
type healthBody struct {
	Status string   `json:"status"`
	Failed []string `json:"failed,omitempty"`
}

// NewHealth returns a Health that logs failed checks to logger. A nil logger
// discards the log output.
func NewHealth(logger *slog.Logger) *Health {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Health{logger: logger, checkTimeout: defaultCheckTimeout}
}

// AddReadinessCheck registers a named readiness check. It may be called at any
// time. A Check that is already running does not run a check registered after
// it started. A nil check counts as failed, so that readiness fails closed.
func (h *Health) AddReadinessCheck(name string, check func(ctx context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks = append(h.checks, readinessCheck{name: name, fn: check})
}

// Check runs every registered readiness check concurrently and returns nil
// when all of them pass. Otherwise it returns an error that joins one failure
// per failed check, in registration order. Each failure names its check.
//
// Every check receives ctx and must return when ctx is done. A check that has
// not returned when ctx is done counts as failed with a timeout error, and
// Check returns at that point without waiting for it. The goroutine of such a
// check is not stopped. It ends when the check function returns, so a check
// that ignores ctx holds its goroutine until it returns. Check starts no other
// goroutine, and every goroutine it starts is bounded by one check function.
// A panicking check counts as failed and does not stop the process.
func (h *Health) Check(ctx context.Context) error {
	checks := h.snapshot()
	if len(checks) == 0 {
		return nil
	}

	// Buffered to len(checks), so that a check that returns after Check has
	// given up can still send its result and end.
	results := make(chan checkResult, len(checks))
	for i, c := range checks {
		go func() {
			results <- checkResult{index: i, err: runCheck(ctx, c)}
		}()
	}

	errs := make([]error, len(checks))
	finished := make([]bool, len(checks))
	record := func(r checkResult) {
		errs[r.index] = r.err
		finished[r.index] = true
	}
wait:
	for range checks {
		select {
		case r := <-results:
			record(r)
		case <-ctx.Done():
			break wait
		}
	}
	// When the context ended, take the results that are already in. The checks
	// that are still running are reported as timed out below.
	for drained := false; !drained; {
		select {
		case r := <-results:
			record(r)
		default:
			drained = true
		}
	}

	var failures []error
	for i, c := range checks {
		err := errs[i]
		if !finished[i] {
			err = fmt.Errorf("no result before the context ended: %w", ctx.Err())
		}
		if err == nil {
			continue
		}
		failures = append(failures, &checkError{name: c.name, err: err})
	}
	return errors.Join(failures...)
}

// LiveHandler always answers 200 {"status":"ok"} while the process runs.
func (h *Health) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, healthBody{Status: "ok"})
	})
}

// ReadyHandler runs Check under a deadline of the default check timeout. It
// answers 200 {"status":"ok"} when all checks pass, and 503
// {"status":"unavailable","failed":[names]} otherwise. Error text is logged at
// warn level by Check and is never included in the response.
func (h *Health) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), h.checkTimeout)
		defer cancel()
		if err := h.Check(ctx); err != nil {
			// Check reports failures to its caller and logs nothing, so that
			// a caller serving its own probe logs each failure once.
			for _, f := range checkFailures(err) {
				h.logger.WarnContext(ctx, "readiness check failed",
					slog.String("check", f.name), slog.Any("error", f.err))
			}
			writeHealth(w, http.StatusServiceUnavailable, healthBody{Status: "unavailable", Failed: failedNames(err)})
			return
		}
		writeHealth(w, http.StatusOK, healthBody{Status: "ok"})
	})
}

// snapshot returns a copy of the registered checks.
func (h *Health) snapshot() []readinessCheck {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return slices.Clone(h.checks)
}

// runCheck calls one check. A nil check and a panic both count as failures,
// so that a bad check fails readiness without taking the process down.
func runCheck(ctx context.Context, c readinessCheck) (err error) {
	if c.fn == nil {
		return errors.New("obs: readiness check is nil")
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("obs: readiness check panicked: %v", r)
		}
	}()
	return c.fn(ctx)
}

// failedNames returns the names of the checks that Check reported, in order.
func failedNames(err error) []string {
	var names []string
	for _, f := range checkFailures(err) {
		names = append(names, f.name)
	}
	return names
}

// checkFailures returns the failed checks that err, a result of Check,
// reports.
func checkFailures(err error) []*checkError {
	switch e := err.(type) {
	case *checkError:
		return []*checkError{e}
	case interface{ Unwrap() []error }:
		var failures []*checkError
		for _, sub := range e.Unwrap() {
			failures = append(failures, checkFailures(sub)...)
		}
		return failures
	default:
		return nil
	}
}

// writeHealth writes body as JSON with the given status code. The body has
// no error path that a client could observe, so the write error is dropped.
func writeHealth(w http.ResponseWriter, status int, body healthBody) {
	buf, err := json.Marshal(body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf)
}
