package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// get performs a GET of path, adding the bearer token when auth is true.
func get(t *testing.T, h http.Handler, path string, auth bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth {
		req.Header.Set("Authorization", "Bearer "+testToken)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPprofHandlerRoutesAndAuth(t *testing.T) {
	t.Parallel()
	h, err := PprofHandler([]byte(testToken))
	if err != nil {
		t.Fatalf("PprofHandler: %v", err)
	}
	tests := []struct {
		name       string
		path       string
		auth       bool
		wantStatus int
		wantBody   string
	}{
		{name: "index needs a token", path: "/unio/debug/pprof/", wantStatus: http.StatusUnauthorized},
		{name: "named profile needs a token", path: "/unio/debug/pprof/heap", wantStatus: http.StatusUnauthorized},
		{name: "cpu profile needs a token", path: "/unio/debug/pprof/profile?seconds=1", wantStatus: http.StatusUnauthorized},
		{name: "index lists profiles", path: "/unio/debug/pprof/", auth: true, wantStatus: http.StatusOK, wantBody: "/unio/debug/pprof/heap"},
		{name: "heap as text", path: "/unio/debug/pprof/heap?debug=1", auth: true, wantStatus: http.StatusOK, wantBody: "heap profile:"},
		{name: "goroutine stacks", path: "/unio/debug/pprof/goroutine?debug=2", auth: true, wantStatus: http.StatusOK, wantBody: "goroutine "},
		{name: "unknown profile", path: "/unio/debug/pprof/nosuch", auth: true, wantStatus: http.StatusNotFound},
		{name: "net/http/pprof symbol endpoint is gone", path: "/unio/debug/pprof/symbol", auth: true, wantStatus: http.StatusNotFound},
		{name: "net/http/pprof cmdline endpoint is gone", path: "/unio/debug/pprof/cmdline", auth: true, wantStatus: http.StatusNotFound},
		{name: "debug value out of range", path: "/unio/debug/pprof/heap?debug=9", auth: true, wantStatus: http.StatusBadRequest},
		{name: "outside the prefix is not mounted", path: "/debug/pprof/", auth: true, wantStatus: http.StatusNotFound},
		{name: "seconds zero", path: "/unio/debug/pprof/profile?seconds=0", auth: true, wantStatus: http.StatusBadRequest},
		{name: "seconds above the limit", path: "/unio/debug/pprof/profile?seconds=61", auth: true, wantStatus: http.StatusBadRequest},
		{name: "seconds not a number", path: "/unio/debug/pprof/trace?seconds=ten", auth: true, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := get(t, h, tc.path, tc.auth)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body does not contain %q", tc.wantBody)
			}
		})
	}
}

// TestPprofProfiles runs the real CPU and trace profiles. The runtime allows one
// of each at a time, so this test is not parallel.
func TestPprofProfiles(t *testing.T) {
	h, err := PprofHandler([]byte(testToken))
	if err != nil {
		t.Fatalf("PprofHandler: %v", err)
	}
	for _, path := range []string{"/unio/debug/pprof/profile?seconds=1", "/unio/debug/pprof/trace?seconds=1"} {
		rec := get(t, h, path, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, body: %s", path, rec.Code, rec.Body.String())
		}
		if rec.Body.Len() == 0 {
			t.Fatalf("%s: empty body", path)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("%s: X-Content-Type-Options = %q", path, got)
		}
	}
}

// TestPprofProfilesAreExclusive checks that a second CPU profile or trace is
// refused while one is in progress, without starting a real profile.
func TestPprofProfilesAreExclusive(t *testing.T) {
	t.Parallel()
	routes := newPprofRoutes()
	if !routes.cpu.acquire() {
		t.Fatal("first acquire failed")
	}
	defer routes.cpu.release()
	if routes.cpu.acquire() {
		t.Fatal("second acquire succeeded while busy")
	}
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unio/debug/pprof/profile?seconds=1", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("cpu profile while busy: status = %d, want 409", rec.Code)
	}

	if !routes.trace.acquire() {
		t.Fatal("trace acquire failed")
	}
	defer routes.trace.release()
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unio/debug/pprof/trace?seconds=1", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("trace while busy: status = %d, want 409", rec.Code)
	}
}

// TestDefaultServeMuxHasNoPprofRoutes guards the reason for not importing
// net/http/pprof: its init would register unauthenticated profiling routes on
// http.DefaultServeMux.
func TestDefaultServeMuxHasNoPprofRoutes(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/profile", "/debug/pprof/trace", "/debug/pprof/goroutine"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if _, pattern := http.DefaultServeMux.Handler(req); pattern != "" {
			t.Errorf("http.DefaultServeMux serves %s through pattern %q", path, pattern)
		}
	}
}

func TestParseProfileSeconds(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		query   string
		def     int
		want    int
		wantErr bool
	}{
		{name: "default when absent", query: "", def: 30, want: 30},
		{name: "explicit", query: "seconds=12", def: 30, want: 12},
		{name: "minimum", query: "seconds=1", def: 30, want: 1},
		{name: "maximum", query: "seconds=60", def: 30, want: 60},
		{name: "zero", query: "seconds=0", def: 30, wantErr: true},
		{name: "above maximum", query: "seconds=61", def: 30, wantErr: true},
		{name: "negative", query: "seconds=-3", def: 30, wantErr: true},
		{name: "not a number", query: "seconds=x", def: 30, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/unio/debug/pprof/profile?"+tc.query, nil)
			got, err := parseProfileSeconds(req, tc.def)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && got.Seconds() != float64(tc.want) {
				t.Fatalf("seconds = %v, want %d", got, tc.want)
			}
		})
	}
}

func TestPprofHandlerConstructorErrors(t *testing.T) {
	t.Parallel()
	if _, err := PprofHandler(nil); err == nil {
		t.Fatal("PprofHandler(nil) error = nil, want error")
	}
}
