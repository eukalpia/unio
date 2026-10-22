package main

import (
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	res := runCLI(t, validEnv(), "version")
	if res.code != 0 {
		t.Fatalf("version exit code = %d, stderr = %s", res.code, res.stderr)
	}
	for _, want := range []string{"unio version dev", "commit: unknown", "go: go1.", "platform: "} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("version output lacks %q:\n%s", want, res.stdout)
		}
	}
}

func TestCollectBuildInfo(t *testing.T) {
	long := "0123456789abcdef0123456789abcdef01234567"
	tests := []struct {
		name        string
		ver, com    string
		dt          string
		bi          *debug.BuildInfo
		wantVersion string
		wantCommit  string
		wantDate    string
	}{
		{name: "no build info", ver: "dev", com: "unknown", dt: "unknown", wantVersion: "dev", wantCommit: "unknown", wantDate: "unknown"},
		{name: "link time values win", ver: "v1.2.3", com: "abc", dt: "2026-01-01",
			bi:          &debug.BuildInfo{Main: debug.Module{Version: "v9.9.9"}},
			wantVersion: "v1.2.3", wantCommit: "abc", wantDate: "2026-01-01"},
		{name: "module version fills in dev", ver: "dev", com: "unknown", dt: "unknown",
			bi:          &debug.BuildInfo{Main: debug.Module{Version: "v0.1.0"}},
			wantVersion: "v0.1.0", wantCommit: "unknown", wantDate: "unknown"},
		{name: "devel module version is ignored", ver: "dev", com: "unknown", dt: "unknown",
			bi:          &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}},
			wantVersion: "dev", wantCommit: "unknown", wantDate: "unknown"},
		{name: "vcs settings are used and shortened", ver: "dev", com: "unknown", dt: "unknown",
			bi: &debug.BuildInfo{Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: long},
				{Key: "vcs.time", Value: "2026-10-22T10:00:00Z"},
			}},
			wantVersion: "dev", wantCommit: long[:12], wantDate: "2026-10-22T10:00:00Z"},
		{name: "short revision kept", ver: "dev", com: "unknown", dt: "unknown",
			bi:          &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "abc"}}},
			wantVersion: "dev", wantCommit: "abc", wantDate: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := collectBuildInfo(tt.ver, tt.com, tt.dt, tt.bi)
			if got.Version != tt.wantVersion || got.Commit != tt.wantCommit || got.Date != tt.wantDate {
				t.Fatalf("collectBuildInfo = %+v", got)
			}
			if got.GoVersion == "" || got.Platform == "" {
				t.Fatalf("runtime fields missing: %+v", got)
			}
		})
	}
}

func TestHealthcheckCommand(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		wantCode int
		wantErr  string
	}{
		{name: "healthy", status: http.StatusOK, wantCode: 0},
		{name: "unavailable", status: http.StatusServiceUnavailable, wantCode: 1, wantErr: "returned 503"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/unio/health/live" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()
			res := runCLI(t, validEnv(), "healthcheck", "--url", srv.URL+"/unio/health/live")
			if res.code != tt.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr = %s", res.code, tt.wantCode, res.stderr)
			}
			if tt.wantErr != "" && !strings.Contains(res.stderr, tt.wantErr) {
				t.Fatalf("stderr = %q, want %q", res.stderr, tt.wantErr)
			}
		})
	}
}

func TestHealthcheckUnreachableAndInvalidURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	target := srv.URL
	srv.Close()

	res := runCLI(t, validEnv(), "healthcheck", "--url", target+"/unio/health/live")
	if res.code == 0 || !strings.Contains(res.stderr, "failed") {
		t.Fatalf("unreachable node: code = %d, stderr = %q", res.code, res.stderr)
	}
	res = runCLI(t, validEnv(), "healthcheck", "--url", "://bad")
	if res.code == 0 {
		t.Fatal("invalid URL must fail")
	}
}

func TestHealthcheckDefaultURL(t *testing.T) {
	if !strings.HasPrefix(defaultHealthURL, "http://127.0.0.1:9000/unio/health/") {
		t.Fatalf("default health URL = %q", defaultHealthURL)
	}
}
