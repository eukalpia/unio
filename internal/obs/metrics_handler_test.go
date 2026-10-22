package obs

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "s3cr3t-token-value-0123"

// recordingHandler returns a handler that counts invocations and writes 200.
func recordingHandler(calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.WriteHeader(http.StatusOK)
	})
}

func TestBearerAuth(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		header     string
		wantStatus int
		wantCalled bool
	}{
		{name: "missing header", header: "", wantStatus: http.StatusUnauthorized},
		{name: "basic scheme with correct secret", header: "Basic " + base64.StdEncoding.EncodeToString([]byte("user:"+testToken)), wantStatus: http.StatusUnauthorized},
		{name: "token without scheme", header: testToken, wantStatus: http.StatusUnauthorized},
		{name: "bearer with no token", header: "Bearer ", wantStatus: http.StatusUnauthorized},
		{name: "wrong token of same length", header: "Bearer " + strings.Repeat("x", len(testToken)), wantStatus: http.StatusUnauthorized},
		{name: "wrong token shorter", header: "Bearer abc", wantStatus: http.StatusUnauthorized},
		{name: "wrong token longer", header: "Bearer " + testToken + "-extra", wantStatus: http.StatusUnauthorized},
		{name: "token with trailing space", header: "Bearer " + testToken + " ", wantStatus: http.StatusUnauthorized},
		{name: "correct token", header: "Bearer " + testToken, wantStatus: http.StatusOK, wantCalled: true},
		{name: "scheme is case insensitive", header: "bearer " + testToken, wantStatus: http.StatusOK, wantCalled: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			h, err := BearerAuth([]byte(testToken), recordingHandler(&calls))
			if err != nil {
				t.Fatalf("BearerAuth: %v", err)
			}
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if called := calls > 0; called != tc.wantCalled {
				t.Fatalf("next handler called = %v, want %v", called, tc.wantCalled)
			}
			if tc.wantStatus == http.StatusUnauthorized {
				if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
					t.Fatalf("WWW-Authenticate = %q, want Bearer", got)
				}
				if body := rec.Body.String(); body != "" {
					t.Fatalf("401 body = %q, want empty", body)
				}
			}
		})
	}
}

func TestBearerAuthConstructorErrors(t *testing.T) {
	t.Parallel()
	ok := recordingHandler(new(int))
	tests := []struct {
		name  string
		token []byte
		next  http.Handler
	}{
		{name: "nil token", token: nil, next: ok},
		{name: "empty token", token: []byte{}, next: ok},
		{name: "nil next handler", token: []byte(testToken), next: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if h, err := BearerAuth(tc.token, tc.next); err == nil || h != nil {
				t.Fatalf("BearerAuth returned (%v, %v), want an error", h, err)
			}
		})
	}
}

func TestMetricsHandler(t *testing.T) {
	t.Parallel()
	t.Run("nil metrics is an error", func(t *testing.T) {
		t.Parallel()
		if _, err := MetricsHandler(nil, []byte(testToken)); err == nil {
			t.Fatal("MetricsHandler(nil) error = nil, want error")
		}
	})
	t.Run("empty token is an error", func(t *testing.T) {
		t.Parallel()
		if _, err := MetricsHandler(newTestMetrics(t), nil); err == nil {
			t.Fatal("MetricsHandler with empty token error = nil, want error")
		}
	})

	m := newTestMetrics(t)
	m.ObserveHTTPRequest("GetObject", 200, time.Millisecond, 1, 2)
	h, err := MetricsHandler(m, []byte(testToken))
	if err != nil {
		t.Fatalf("MetricsHandler: %v", err)
	}

	tests := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{name: "unauthenticated", header: "", wantStatus: http.StatusUnauthorized},
		{name: "wrong token", header: "Bearer nope", wantStatus: http.StatusUnauthorized},
		{name: "authenticated", header: "Bearer " + testToken, wantStatus: http.StatusOK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			body := rec.Body.String()
			for _, want := range []string{"unio_http_requests_total", "unio_gc_queue_depth", "go_goroutines"} {
				if !strings.Contains(body, want) {
					t.Fatalf("exposition does not contain %s", want)
				}
			}
		})
	}
}
