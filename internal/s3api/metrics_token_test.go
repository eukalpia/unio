package s3api_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// metricsToken is a configured scrape token of a realistic length. wrongSameLen
// differs from it in the last byte only.
const (
	metricsToken = "scrape-token-0123456789abcdef-0123"
	wrongSameLen = "scrape-token-0123456789abcdef-012X"
)

// metricsHook is the metrics handler the tests serve.
var metricsHook = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("unio_up 1\n"))
})

// countingAuthn counts the requests that reach SigV4 verification.
type countingAuthn struct {
	inner fakes.Authenticator
	calls atomic.Int64
}

// Authenticate counts the call and delegates to the fake.
func (a *countingAuthn) Authenticate(ctx context.Context, req *auth.Request) (*auth.Result, error) {
	a.calls.Add(1)
	return a.inner.Authenticate(ctx, req)
}

// syncBuffer is a log sink that several server goroutines may write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer.
func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// String returns everything written so far.
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// denyAdmin is an authorizer that refuses the admin action and allows the rest.
func denyAdmin() *fakes.Authorizer {
	return &fakes.Authorizer{Deny: func(_ auth.Identity, action auth.Action, _, _ string) bool {
		return action == auth.ActionAdmin
	}}
}

// adminCalls counts the admin checks recorded by authz.
func adminCalls(authz *fakes.Authorizer) int {
	n := 0
	for _, c := range authz.Calls() {
		if c.Action == auth.ActionAdmin {
			n++
		}
	}
	return n
}

// TestMetricsBearerToken checks how the Bearer path judges each credential. A
// Bearer header is never handed to SigV4, so the authenticator must not be
// called for any of these requests, and every failure is 403 AccessDenied.
func TestMetricsBearerToken(t *testing.T) {
	cases := []struct {
		name       string
		header     string
		wantStatus int
	}{
		{"right token", "Bearer " + metricsToken, http.StatusOK},
		{"lowercase scheme", "bearer " + metricsToken, http.StatusOK},
		{"uppercase scheme", "BEARER " + metricsToken, http.StatusOK},
		{"several spaces before the token", "Bearer   " + metricsToken, http.StatusOK},
		{"wrong token, same length", "Bearer " + wrongSameLen, http.StatusForbidden},
		{"wrong token, longer", "Bearer " + metricsToken + "x", http.StatusForbidden},
		{"wrong token, prefix", "Bearer " + metricsToken[:10], http.StatusForbidden},
		{"empty bearer", "Bearer ", http.StatusForbidden},
		{"bearer with no space", "Bearer", http.StatusForbidden},
		{"scheme run into the token", "Bearer" + metricsToken, http.StatusForbidden},
		{"tab instead of space", "Bearer\t" + metricsToken, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			authn := &countingAuthn{}
			authz := &fakes.Authorizer{}
			base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: []byte(metricsToken)}, authn, authz, nil)
			status, body := do(t, newHTTPClient(t, nil), "GET", base+"/unio/metrics", map[string]string{"Authorization": tc.header})
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %q)", status, tc.wantStatus, body)
			}
			if tc.wantStatus == http.StatusOK && body != "unio_up 1\n" {
				t.Fatalf("body = %q", body)
			}
			if tc.wantStatus == http.StatusForbidden && !strings.Contains(body, "AccessDenied") {
				t.Fatalf("body = %q, want AccessDenied", body)
			}
			if n := authn.calls.Load(); n != 0 {
				t.Fatalf("SigV4 authenticator called %d times for a Bearer request", n)
			}
		})
	}
}

// TestMetricsBearerRightTokenSkipsAuthorization checks that a valid token is
// enough: the admin action is not consulted for it.
func TestMetricsBearerRightTokenSkipsAuthorization(t *testing.T) {
	authz := &fakes.Authorizer{}
	base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: []byte(metricsToken)}, nil, authz, nil)
	status, _ := do(t, newHTTPClient(t, nil), "GET", base+"/unio/metrics", map[string]string{"Authorization": "Bearer " + metricsToken})
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if n := len(authz.Calls()); n != 0 {
		t.Fatalf("authorizer called %d times for a valid bearer token", n)
	}
}

// TestMetricsBearerWithoutConfiguredToken checks that a Bearer header is 403
// on a node with no metrics token, and that SigV4 is never tried.
func TestMetricsBearerWithoutConfiguredToken(t *testing.T) {
	authn := &countingAuthn{}
	base := startServerWith(t, s3api.Config{Metrics: metricsHook}, authn, nil, nil)
	hc := newHTTPClient(t, nil)
	for _, header := range []string{"Bearer " + metricsToken, "Bearer ", "Bearer"} {
		status, body := do(t, hc, "GET", base+"/unio/metrics", map[string]string{"Authorization": header})
		if status != http.StatusForbidden || !strings.Contains(body, "AccessDenied") {
			t.Fatalf("%q: status %d body %q, want 403 AccessDenied", header, status, body)
		}
	}
	if n := authn.calls.Load(); n != 0 {
		t.Fatalf("SigV4 authenticator called %d times", n)
	}
}

// TestMetricsSigV4AdminStillWorks checks that a request without a Bearer
// header takes the SigV4 path and needs the admin action, with or without a
// metrics token configured.
func TestMetricsSigV4AdminStillWorks(t *testing.T) {
	const signed = "AWS4-HMAC-SHA256 Credential=AKIAFAKE0000000000/20261022/us-east-1/s3/aws4_request"
	for _, tok := range [][]byte{nil, []byte(metricsToken)} {
		authn := &countingAuthn{}
		authz := &fakes.Authorizer{}
		base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: tok}, authn, authz, nil)
		status, body := do(t, newHTTPClient(t, nil), "GET", base+"/unio/metrics", map[string]string{"Authorization": signed})
		if status != http.StatusOK || body != "unio_up 1\n" {
			t.Fatalf("token %q: status %d body %q", tok, status, body)
		}
		if n := authn.calls.Load(); n != 1 {
			t.Fatalf("SigV4 authenticator called %d times, want 1", n)
		}
		if n := adminCalls(authz); n != 1 {
			t.Fatalf("admin action checked %d times, want 1", n)
		}
	}
}

// TestMetricsSigV4NonAdminIsDenied checks that a signed user without the admin
// action gets 403, even when the metrics token is configured.
func TestMetricsSigV4NonAdminIsDenied(t *testing.T) {
	const signed = "AWS4-HMAC-SHA256 Credential=AKIAFAKE0000000000/20261022/us-east-1/s3/aws4_request"
	base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: []byte(metricsToken)}, nil, denyAdmin(), nil)
	status, body := do(t, newHTTPClient(t, nil), "GET", base+"/unio/metrics", map[string]string{"Authorization": signed})
	if status != http.StatusForbidden || !strings.Contains(body, "AccessDenied") {
		t.Fatalf("status %d body %q, want 403 AccessDenied", status, body)
	}
}

// TestMetricsTokenIsCopiedAtNew checks that changing the caller's slice after
// New has no effect on the token the server accepts.
func TestMetricsTokenIsCopiedAtNew(t *testing.T) {
	tok := []byte(metricsToken)
	base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: tok}, nil, nil, nil)
	tok[0] = 'X'
	hc := newHTTPClient(t, nil)
	if status, _ := do(t, hc, "GET", base+"/unio/metrics", map[string]string{"Authorization": "Bearer " + metricsToken}); status != http.StatusOK {
		t.Fatalf("original token after mutation: status %d, want 200", status)
	}
	if status, _ := do(t, hc, "GET", base+"/unio/metrics", map[string]string{"Authorization": "Bearer " + string(tok)}); status != http.StatusForbidden {
		t.Fatalf("mutated token: status %d, want 403", status)
	}
}

// TestMetricsTokenNeverLogged captures every log record at debug level while
// the endpoint sees right, wrong and empty bearer tokens and a SigV4 request,
// and checks that neither the token nor any Authorization value appears.
func TestMetricsTokenNeverLogged(t *testing.T) {
	const wrong = "attacker-guess-0123456789abcdef-zzzz"
	var logs syncBuffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	base := startServerWith(t, s3api.Config{Metrics: metricsHook, MetricsToken: []byte(metricsToken)}, nil, nil, logger)
	hc := newHTTPClient(t, nil)
	headers := []string{
		"Bearer " + metricsToken,
		"Bearer " + wrong,
		"Bearer ",
		"AWS4-HMAC-SHA256 Credential=AKIAFAKE0000000000/20261022/us-east-1/s3/aws4_request",
	}
	for _, h := range headers {
		_, _ = do(t, hc, "GET", base+"/unio/metrics", map[string]string{"Authorization": h})
	}
	out := logs.String()
	if !strings.Contains(out, "request") {
		t.Fatalf("no access log was captured; the test would prove nothing:\n%s", out)
	}
	for _, secret := range []string{metricsToken, wrong, "Bearer", "AKIAFAKE0000000000"} {
		if strings.Contains(out, secret) {
			t.Fatalf("logs contain %q:\n%s", secret, out)
		}
	}
}

// TestMetricsUnsetIsNotImplementedForBearer checks that the NotImplemented
// answer of a node without metrics comes before any token check.
func TestMetricsUnsetIsNotImplementedForBearer(t *testing.T) {
	base := startServerWith(t, s3api.Config{MetricsToken: []byte(metricsToken)}, nil, nil, nil)
	status, body := do(t, newHTTPClient(t, nil), "GET", base+"/unio/metrics", map[string]string{"Authorization": "Bearer " + metricsToken})
	if status != http.StatusNotImplemented || !strings.Contains(body, "NotImplemented") {
		t.Fatalf("status %d body %q", status, body)
	}
}
