package obs

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"
)

// defaultCheckTimeout bounds each readiness check.
const defaultCheckTimeout = 2 * time.Second

// Health serves liveness and readiness. Checks are registered with
// AddReadinessCheck, and the handlers are then served.
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

// AddReadinessCheck registers a named readiness check. The check should
// return when ctx is done, because each check runs under its own timeout.
// Register checks before serving. A nil check counts as failed, so that
// readiness fails closed.
func (h *Health) AddReadinessCheck(name string, check func(ctx context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks = append(h.checks, readinessCheck{name: name, fn: check})
}

// LiveHandler always answers 200 {"status":"ok"} while the process runs.
func (h *Health) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeHealth(w, http.StatusOK, healthBody{Status: "ok"})
	})
}

// ReadyHandler runs every readiness check in registration order, each with
// its own timeout. It answers 200 {"status":"ok"} when all checks pass, and
// 503 {"status":"unavailable","failed":[names]} otherwise. Error text is
// logged at warn level and never included in the response.
func (h *Health) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var failed []string
		for _, c := range h.snapshot() {
			if err := h.runCheck(r.Context(), c); err != nil {
				h.logger.WarnContext(r.Context(), "readiness check failed",
					slog.String("check", c.name), slog.Any("error", err))
				failed = append(failed, c.name)
			}
		}
		if len(failed) > 0 {
			writeHealth(w, http.StatusServiceUnavailable, healthBody{Status: "unavailable", Failed: failed})
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

// runCheck runs one check under a timeout derived from parent.
func (h *Health) runCheck(parent context.Context, c readinessCheck) error {
	if c.fn == nil {
		return errors.New("obs: readiness check is nil")
	}
	ctx, cancel := context.WithTimeout(parent, h.checkTimeout)
	defer cancel()
	return c.fn(ctx)
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
