package obs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// errDiskGone is the cause used by the failing checks in these tests.
var errDiskGone = errors.New("disk gone")

// hungCheck returns a check that ignores its context and returns only after
// release is closed. It closes returned once it has returned.
func hungCheck(release <-chan struct{}, returned chan<- struct{}) func(context.Context) error {
	return func(context.Context) error {
		defer close(returned)
		<-release
		return nil
	}
}

func TestHealthCheck(t *testing.T) {
	t.Parallel()
	pass := func(context.Context) error { return nil }
	fail := func(context.Context) error { return errDiskGone }
	panics := func(context.Context) error { panic("boom") }

	tests := []struct {
		name       string
		checks     []namedCheck
		wantFailed []string
	}{
		{
			name: "no checks passes",
		},
		{
			name:   "all pass",
			checks: []namedCheck{{"a", pass}, {"b", pass}},
		},
		{
			name:       "one fails",
			checks:     []namedCheck{{"a", pass}, {"disk", fail}},
			wantFailed: []string{"disk"},
		},
		{
			name:       "several fail in registration order",
			checks:     []namedCheck{{"c", fail}, {"a", pass}, {"b", fail}},
			wantFailed: []string{"c", "b"},
		},
		{
			name:       "nil check fails",
			checks:     []namedCheck{{"unset", nil}},
			wantFailed: []string{"unset"},
		},
		{
			name:       "panic fails only that check",
			checks:     []namedCheck{{"panics", panics}, {"ok", pass}},
			wantFailed: []string{"panics"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := NewHealth(nil)
			for _, c := range tc.checks {
				h.AddReadinessCheck(c.name, c.fn)
			}
			err := h.Check(context.Background())
			if (err == nil) != (len(tc.wantFailed) == 0) {
				t.Fatalf("Check() = %v, want failure = %v", err, len(tc.wantFailed) > 0)
			}
			if diff := cmp.Diff(tc.wantFailed, failedNames(err)); diff != "" {
				t.Fatalf("failed names mismatch (-want +got):\n%s", diff)
			}
			for _, name := range tc.wantFailed {
				if want := fmt.Sprintf("readiness check %q", name); !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not name the check %q", err, name)
				}
			}
		})
	}
}

func TestHealthCheckKeepsCause(t *testing.T) {
	t.Parallel()
	h := NewHealth(nil)
	h.AddReadinessCheck("disk", func(context.Context) error { return errDiskGone })
	if err := h.Check(context.Background()); !errors.Is(err, errDiskGone) {
		t.Fatalf("Check() = %v, want the cause to be reachable with errors.Is", err)
	}
}

func TestHealthCheckHungCheckReturnsAtDeadline(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	returned := make(chan struct{})
	h := NewHealth(nil)
	h.AddReadinessCheck("pass", func(context.Context) error { return nil })
	h.AddReadinessCheck("hung", hungCheck(release, returned))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Check(ctx) }()

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Check did not return after its context ended")
	}
	// The hung check is still running. Let it return, so that its goroutine
	// ends before the test binary checks for leaks.
	close(release)
	<-returned

	if diff := cmp.Diff([]string{"hung"}, failedNames(err)); diff != "" {
		t.Fatalf("failed names mismatch (-want +got):\n%s", diff)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Check() = %v, want a deadline error for the hung check", err)
	}
}

func TestHealthCheckRunsChecksConcurrently(t *testing.T) {
	t.Parallel()
	const n = 4
	started := make([]chan struct{}, n)
	for i := range started {
		started[i] = make(chan struct{})
	}
	h := NewHealth(nil)
	for i := range n {
		h.AddReadinessCheck(fmt.Sprint(i), func(ctx context.Context) error {
			close(started[i])
			// Each check waits until all of them have started. A sequential
			// run would wait here until ctx ends, and the check would fail.
			for _, s := range started {
				select {
				case <-s:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Check(ctx); err != nil {
		t.Fatalf("checks did not run concurrently: %v", err)
	}
}

func TestHealthCheckConcurrentWithRegistration(t *testing.T) {
	t.Parallel()
	h := NewHealth(nil)
	h.AddReadinessCheck("base", func(context.Context) error { return nil })

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 100 {
			h.AddReadinessCheck(fmt.Sprintf("late-%d", i), func(context.Context) error { return nil })
		}
	}()
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := h.Check(context.Background()); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := len(h.snapshot()); got != 101 {
		t.Fatalf("registered checks = %d, want 101", got)
	}
}

func TestReadyHandlerHungCheckAnswersAtTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	returned := make(chan struct{})
	h := NewHealth(nil)
	h.checkTimeout = 20 * time.Millisecond
	h.AddReadinessCheck("stuck", hungCheck(release, returned))

	code, body, _ := serve(t, h.ReadyHandler())
	close(release)
	<-returned

	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", code)
	}
	if diff := cmp.Diff([]string{"stuck"}, body.Failed); diff != "" {
		t.Fatalf("failed mismatch (-want +got):\n%s", diff)
	}
}
