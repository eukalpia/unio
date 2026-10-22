package obs

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPprofHandler(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		path       string
		auth       bool
		wantStatus int
		wantBody   string
	}{
		{name: "index unauthenticated", path: "/unio/debug/pprof/", wantStatus: http.StatusUnauthorized},
		{name: "cmdline unauthenticated", path: "/unio/debug/pprof/cmdline", wantStatus: http.StatusUnauthorized},
		{name: "profile unauthenticated", path: "/unio/debug/pprof/profile", wantStatus: http.StatusUnauthorized},
		{name: "symbol unauthenticated", path: "/unio/debug/pprof/symbol", wantStatus: http.StatusUnauthorized},
		{name: "trace unauthenticated", path: "/unio/debug/pprof/trace", wantStatus: http.StatusUnauthorized},
		{name: "named profile unauthenticated", path: "/unio/debug/pprof/heap", wantStatus: http.StatusUnauthorized},
		{name: "index authenticated", path: "/unio/debug/pprof/", auth: true, wantStatus: http.StatusOK, wantBody: "Types of profiles available"},
		{name: "cmdline authenticated", path: "/unio/debug/pprof/cmdline", auth: true, wantStatus: http.StatusOK},
		{name: "named heap profile authenticated", path: "/unio/debug/pprof/heap?debug=1", auth: true, wantStatus: http.StatusOK, wantBody: "heap profile:"},
		{name: "unknown profile authenticated", path: "/unio/debug/pprof/nosuch", auth: true, wantStatus: http.StatusNotFound},
		{name: "outside the prefix is not mounted", path: "/debug/pprof/", auth: true, wantStatus: http.StatusNotFound},
	}
	h, err := PprofHandler([]byte(testToken))
	if err != nil {
		t.Fatalf("PprofHandler: %v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.auth {
				req.Header.Set("Authorization", "Bearer "+testToken)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body does not contain %q", tc.wantBody)
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
