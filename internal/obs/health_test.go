package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// healthResponse is the JSON body served by the live and ready handlers.
type healthResponse struct {
	Status string   `json:"status"`
	Failed []string `json:"failed,omitempty"`
}

// namedCheck pairs a readiness check name with its function.
type namedCheck struct {
	name string
	fn   func(context.Context) error
}

func serve(t *testing.T, h http.Handler) (int, healthResponse, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q is not JSON: %v", rec.Body.String(), err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	return rec.Code, body, rec.Body.String()
}

func TestHealthLive(t *testing.T) {
	t.Parallel()
	h := NewHealth(nil)
	h.AddReadinessCheck("broken", func(context.Context) error { return errors.New("down") })
	code, body, _ := serve(t, h.LiveHandler())
	if code != http.StatusOK {
		t.Fatalf("live status = %d, want 200", code)
	}
	if body.Status != "ok" || len(body.Failed) != 0 {
		t.Fatalf("live body = %+v, want status ok", body)
	}
}

func TestHealthReady(t *testing.T) {
	t.Parallel()
	pass := func(context.Context) error { return nil }
	fail := func(context.Context) error { return errors.New("disk gone") }
	blockUntilDone := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	requireDeadline := func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("no deadline")
		}
		return nil
	}

	tests := []struct {
		name            string
		checks          []namedCheck
		wantStatus      int
		wantStatusField string
		wantFailed      []string
	}{
		{
			name:            "no checks is ready",
			wantStatus:      http.StatusOK,
			wantStatusField: "ok",
		},
		{
			name:            "all passing is ready",
			checks:          []namedCheck{{"a", pass}, {"b", pass}},
			wantStatus:      http.StatusOK,
			wantStatusField: "ok",
		},
		{
			name:            "one failing check",
			checks:          []namedCheck{{"a", pass}, {"raft", fail}},
			wantStatus:      http.StatusServiceUnavailable,
			wantStatusField: "unavailable",
			wantFailed:      []string{"raft"},
		},
		{
			name:            "several failures reported in registration order",
			checks:          []namedCheck{{"a", fail}, {"b", pass}, {"c", fail}},
			wantStatus:      http.StatusServiceUnavailable,
			wantStatusField: "unavailable",
			wantFailed:      []string{"a", "c"},
		},
		{
			name:            "nil check fails closed",
			checks:          []namedCheck{{"unset", nil}},
			wantStatus:      http.StatusServiceUnavailable,
			wantStatusField: "unavailable",
			wantFailed:      []string{"unset"},
		},
		{
			name:            "check blocking until its context ends is reported failed",
			checks:          []namedCheck{{"stuck", blockUntilDone}},
			wantStatus:      http.StatusServiceUnavailable,
			wantStatusField: "unavailable",
			wantFailed:      []string{"stuck"},
		},
		{
			name:            "each check receives a deadline",
			checks:          []namedCheck{{"deadline", requireDeadline}},
			wantStatus:      http.StatusOK,
			wantStatusField: "ok",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHealth(nil)
			h.checkTimeout = 20 * time.Millisecond
			for _, c := range tc.checks {
				h.AddReadinessCheck(c.name, c.fn)
			}
			code, body, _ := serve(t, h.ReadyHandler())
			if code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", code, tc.wantStatus)
			}
			if body.Status != tc.wantStatusField {
				t.Fatalf("status field = %q, want %q", body.Status, tc.wantStatusField)
			}
			if diff := cmp.Diff(tc.wantFailed, body.Failed); diff != "" {
				t.Fatalf("failed mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHealthReadyDoesNotLeakErrorText(t *testing.T) {
	t.Parallel()
	h := NewHealth(nil)
	h.AddReadinessCheck("meta", func(context.Context) error {
		return errors.New("dial tcp 10.0.0.5:9000: password=hunter2")
	})
	code, _, raw := serve(t, h.ReadyHandler())
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	for _, leak := range []string{"hunter2", "10.0.0.5", "dial tcp"} {
		if strings.Contains(raw, leak) {
			t.Fatalf("response body leaks %q: %s", leak, raw)
		}
	}
}

func TestHealthReadyLogsFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		fn         func(context.Context) error
		wantLogged bool
	}{
		{name: "failure is logged at warn", fn: func(context.Context) error { return errors.New("replica lag") }, wantLogged: true},
		{name: "success is not logged", fn: func(context.Context) error { return nil }, wantLogged: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			h := NewHealth(logger)
			h.AddReadinessCheck("replication", tc.fn)
			serve(t, h.ReadyHandler())
			out := buf.String()
			if !tc.wantLogged {
				if out != "" {
					t.Fatalf("unexpected log output: %s", out)
				}
				return
			}
			for _, want := range []string{`"level":"WARN"`, `"check":"replication"`, "replica lag"} {
				if !strings.Contains(out, want) {
					t.Fatalf("log output %q does not contain %s", out, want)
				}
			}
		})
	}
}

func TestHealthNilLoggerDoesNotPanic(t *testing.T) {
	t.Parallel()
	h := NewHealth(nil)
	h.AddReadinessCheck("down", func(context.Context) error { return errors.New("x") })
	if code, _, _ := serve(t, h.ReadyHandler()); code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
}
