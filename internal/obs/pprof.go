package obs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// pprofPrefix is the public path prefix of the profiling endpoints.
const pprofPrefix = "/unio/debug/pprof/"

// Profile durations. The CPU profile and the execution trace are bounded so that
// one request cannot hold the runtime's profiler for long.
const (
	defaultCPUSeconds   = 30
	defaultTraceSeconds = 5
	maxProfileSeconds   = 60
)

// PprofHandler serves runtime profiles under /unio/debug/pprof/, behind
// BearerAuth. It serves the named profiles (heap, goroutine, allocs, block,
// mutex, threadcreate), a CPU profile with ?seconds=N, and an execution trace
// with ?seconds=N. Durations are capped at 60 seconds.
//
// The handler is built on runtime/pprof and runtime/trace and does not import
// net/http/pprof. That package registers unauthenticated routes on
// http.DefaultServeMux as a side effect of its init, which must not happen.
//
// The caller mounts the handler only when pprof is enabled, which is off by default.
func PprofHandler(token []byte) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.Handle(pprofPrefix, newPprofRoutes())
	return BearerAuth(token, mux)
}

// exclusive admits at most one holder at a time. The runtime itself allows one
// CPU profile and one execution trace, so a second request is refused rather
// than queued.
type exclusive struct {
	busy atomic.Bool
}

// acquire takes the slot and reports whether it was free.
func (e *exclusive) acquire() bool { return e.busy.CompareAndSwap(false, true) }

// release frees the slot.
func (e *exclusive) release() { e.busy.Store(false) }

// pprofRoutes serves the profiles. It is one value per handler, so the
// exclusive slots are not global state.
type pprofRoutes struct {
	cpu   exclusive
	trace exclusive
}

// newPprofRoutes returns routes with both slots free.
func newPprofRoutes() *pprofRoutes { return &pprofRoutes{} }

// ServeHTTP dispatches on the path below pprofPrefix.
func (p *pprofRoutes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, pprofPrefix)
	switch name {
	case "":
		p.index(w)
	case "profile":
		p.cpuProfile(w, r)
	case "trace":
		p.executionTrace(w, r)
	default:
		p.namedProfile(w, r, name)
	}
}

// index lists the profiles this handler serves.
func (p *pprofRoutes) index(w http.ResponseWriter) {
	var b strings.Builder
	b.WriteString("Profiles (one per line; add ?debug=1 for text, ?debug=2 for goroutine stacks):\n")
	for _, prof := range pprof.Profiles() {
		b.WriteString(pprofPrefix + prof.Name() + "\n")
	}
	b.WriteString(pprofPrefix + "profile?seconds=N   CPU profile, N from 1 to 60\n")
	b.WriteString(pprofPrefix + "trace?seconds=N     execution trace, N from 1 to 60\n")
	setProfileHeaders(w, "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// namedProfile writes a profile registered with runtime/pprof.
func (p *pprofRoutes) namedProfile(w http.ResponseWriter, r *http.Request, name string) {
	prof := pprof.Lookup(name)
	if prof == nil {
		http.NotFound(w, r)
		return
	}
	debug, err := parseDebug(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	contentType := "application/octet-stream"
	if debug > 0 {
		contentType = "text/plain; charset=utf-8"
	}
	setProfileHeaders(w, contentType)
	w.WriteHeader(http.StatusOK)
	// The status is already sent, so a write error can only end the body early.
	_ = prof.WriteTo(w, debug)
}

// cpuProfile records a CPU profile for the requested duration.
func (p *pprofRoutes) cpuProfile(w http.ResponseWriter, r *http.Request) {
	d, err := parseProfileSeconds(r, defaultCPUSeconds)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !p.cpu.acquire() {
		http.Error(w, "a CPU profile is already in progress", http.StatusConflict)
		return
	}
	defer p.cpu.release()

	// Headers must be set before Start: the profiler writes to w from its own
	// goroutine, and the first write sends the headers.
	setProfileHeaders(w, "application/octet-stream")
	if err := pprof.StartCPUProfile(w); err != nil {
		// Another profiler in the process holds the runtime's CPU profile.
		http.Error(w, "a CPU profile is already in progress", http.StatusConflict)
		return
	}
	defer pprof.StopCPUProfile()
	_ = waitFor(r.Context(), d)
}

// executionTrace records a runtime execution trace for the requested duration.
func (p *pprofRoutes) executionTrace(w http.ResponseWriter, r *http.Request) {
	d, err := parseProfileSeconds(r, defaultTraceSeconds)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !p.trace.acquire() {
		http.Error(w, "an execution trace is already in progress", http.StatusConflict)
		return
	}
	defer p.trace.release()

	setProfileHeaders(w, "application/octet-stream")
	if err := trace.Start(w); err != nil {
		http.Error(w, "an execution trace is already in progress", http.StatusConflict)
		return
	}
	defer trace.Stop()
	_ = waitFor(r.Context(), d)
}

// waitFor blocks for d or until ctx is done, whichever comes first.
func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// setProfileHeaders sets the content type and forbids MIME sniffing. Callers
// set it before the first write.
func setProfileHeaders(w http.ResponseWriter, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// parseDebug reads the ?debug= parameter of a named profile. It accepts 0, 1 or 2.
func parseDebug(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("debug")
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 2 {
		return 0, errors.New("debug must be 0, 1 or 2")
	}
	return n, nil
}

// parseProfileSeconds reads ?seconds= and returns the duration. It returns def
// when the parameter is absent, and an error outside 1 to 60.
func parseProfileSeconds(r *http.Request, def int) (time.Duration, error) {
	raw := r.URL.Query().Get("seconds")
	if raw == "" {
		return time.Duration(def) * time.Second, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxProfileSeconds {
		return 0, fmt.Errorf("seconds must be an integer from 1 to %d", maxProfileSeconds)
	}
	return time.Duration(n) * time.Second, nil
}
