package s3api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eukalpia/unio/internal/admin"
	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/clustertest"
	"github.com/eukalpia/unio/internal/console"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3api"
)

// bridgeResponse is the status, header and body of one HTTP exchange.
type bridgeResponse struct {
	status int
	header http.Header
	body   string
}

// doHost sends one request with an explicit Host header and an optional body.
func doHost(t *testing.T, hc *http.Client, method, url, host string, headers map[string]string, body string) bridgeResponse {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if host != "" {
		req.Host = host
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return bridgeResponse{status: resp.StatusCode, header: resp.Header, body: string(b)}
}

// fakeDeletionQueue reports an empty deletion backlog.
type fakeDeletionQueue struct{}

// Stats implements admin.Deletions.
func (fakeDeletionQueue) Stats() engine.Stats { return engine.Stats{} }

// bridgeAdmin builds an admin service over a real IAM store with one admin user,
// and returns it with the user's access key and secret.
func bridgeAdmin(t *testing.T) (*admin.Service, string, string) {
	t.Helper()
	store, err := iam.New(authtest.NewMemKV(), iam.Config{
		KEK:           iam.DeriveKEK([]byte("cluster-secret-for-bridge-tests-0123456789ab")),
		RootAccessKey: "UNIOBRIDGETEST01",
		RootSecret:    "bridge-root-secret-0123456789-ABCDEFGHIJ",
		Region:        "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.CreateUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAccessKey(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetUserPolicy(ctx, "alice", []byte(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"unio:Admin","Resource":"*"}]}`)); err != nil {
		t.Fatal(err)
	}
	svc, err := admin.New(admin.Config{
		IAM:           store,
		Credentials:   store,
		Layer:         objecttest.NewLayer(),
		Cluster:       clustertest.NewFakeTopology("node-a", &cluster.Map{ClusterID: "bridge"}),
		Deletions:     fakeDeletionQueue{},
		Recorder:      admin.NewRecorder(nil),
		BufferInUse:   func() int64 { return 0 },
		RootAccessKey: "UNIOBRIDGETEST01",
		SessionSecret: []byte("cluster-secret-for-bridge-tests-0123456789ab"),
		Region:        "us-east-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, key.ID, key.Secret
}

// consoleFS is a small console build for the bridge tests.
func consoleFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><title>bridge console</title>")},
		"assets/app-AbC12x.js": {Data: []byte("console.log('ok')")},
	}
}

func TestConsoleIsServedUnderTheReservedPath(t *testing.T) {
	h, err := console.NewFS(consoleFS())
	if err != nil {
		t.Fatal(err)
	}
	base := startServerWith(t, s3api.Config{Console: h}, nil, nil, nil)
	hc := newHTTPClient(t, nil)

	r := doHost(t, hc, http.MethodGet, base+"/unio/console/", "", nil, "")
	if r.status != http.StatusOK || !strings.Contains(r.body, "bridge console") {
		t.Fatalf("index: status %d body %q", r.status, r.body)
	}
	if got := r.header.Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") || !strings.Contains(got, "frame-ancestors 'none'") {
		t.Errorf("CSP = %q", got)
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" || r.header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("security headers missing: %v", r.header)
	}
	if got := r.header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index Cache-Control = %q", got)
	}

	r = doHost(t, hc, http.MethodGet, base+"/unio/console/assets/app-AbC12x.js", "", nil, "")
	if r.status != http.StatusOK || r.body != "console.log('ok')" {
		t.Fatalf("bundle: status %d body %q", r.status, r.body)
	}
	if r.header.Get("Content-Type") != "text/javascript; charset=utf-8" || r.header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("bundle headers = %v", r.header)
	}

	r = doHost(t, hc, http.MethodGet, base+"/unio/console/buckets/photos", "", nil, "")
	if r.status != http.StatusOK || !strings.Contains(r.body, "bridge console") {
		t.Errorf("SPA fallback: status %d body %q", r.status, r.body)
	}

	r = doHost(t, hc, http.MethodHead, base+"/unio/console/", "", nil, "")
	if r.status != http.StatusOK || r.body != "" || r.header.Get("Content-Length") == "" {
		t.Errorf("HEAD: status %d body %q length %q", r.status, r.body, r.header.Get("Content-Length"))
	}

	r = doHost(t, hc, http.MethodPost, base+"/unio/console/", "", nil, "x")
	if r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: status %d allow %q", r.status, r.header.Get("Allow"))
	}
}

func TestConsoleTraversalIsNotFound(t *testing.T) {
	h, err := console.NewFS(consoleFS())
	if err != nil {
		t.Fatal(err)
	}
	base := startServerWith(t, s3api.Config{Console: h}, nil, nil, nil)
	hc := newHTTPClient(t, nil)
	for _, p := range []string{
		"/unio/console/../../etc/passwd",
		"/unio/console/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/unio/console/..%2f..%2fetc%2fpasswd",
		"/unio/console/%252e%252e/%252e%252e/etc/passwd",
	} {
		r := doHost(t, hc, http.MethodGet, base+p, "", nil, "")
		if r.status != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, r.status)
		}
		if strings.Contains(r.body, "bridge console") {
			t.Errorf("%s: the fallback page was served", p)
		}
	}
}

func TestConsoleAbsentIsNotFound(t *testing.T) {
	base := startServerWith(t, s3api.Config{}, nil, nil, nil)
	hc := newHTTPClient(t, nil)
	r := doHost(t, hc, http.MethodGet, base+"/unio/console/", "", nil, "")
	if r.status != http.StatusNotFound {
		t.Errorf("unconfigured console: status %d, want 404", r.status)
	}
}

func TestAdminAPIOverTheS3Listener(t *testing.T) {
	svc, key, secret := bridgeAdmin(t)
	base := startServerWith(t, s3api.Config{Admin: svc}, nil, nil, nil)
	hc := newHTTPClient(t, nil)

	// Log in: the body is read, the cookie is set and the CSRF token returned.
	login, err := json.Marshal(map[string]string{"accessKey": key, "secretKey": secret})
	if err != nil {
		t.Fatal(err)
	}
	r := doHost(t, hc, http.MethodPost, base+"/unio/admin/v1/session", "", map[string]string{"Content-Type": "application/json"}, string(login))
	if r.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", r.status, r.body)
	}
	cookies := r.header.Values("Set-Cookie")
	if len(cookies) != 1 || !strings.Contains(cookies[0], "unio_session=") || !strings.Contains(cookies[0], "HttpOnly") {
		t.Fatalf("Set-Cookie = %v", cookies)
	}
	var session struct {
		CSRF string `json:"csrf"`
	}
	if err := json.Unmarshal([]byte(r.body), &session); err != nil || session.CSRF == "" {
		t.Fatalf("login body %s: %v", r.body, err)
	}
	cookie := strings.SplitN(strings.TrimPrefix(cookies[0], "unio_session="), ";", 2)[0]

	auth := map[string]string{"Cookie": "unio_session=" + cookie}
	with := func(extra map[string]string) map[string]string {
		m := map[string]string{}
		for k, v := range auth {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// A state change needs the CSRF token and a JSON body.
	r = doHost(t, hc, http.MethodPost, base+"/unio/admin/v1/buckets", "", with(map[string]string{"Content-Type": "application/json"}), `{"name":"bridge-bucket"}`)
	if r.status != http.StatusForbidden {
		t.Errorf("create without CSRF: status %d, want 403", r.status)
	}
	r = doHost(t, hc, http.MethodPost, base+"/unio/admin/v1/buckets", "", with(map[string]string{
		"Content-Type": "application/json", "X-Unio-CSRF": session.CSRF,
	}), `{"name":"bridge-bucket"}`)
	if r.status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.status, r.body)
	}

	r = doHost(t, hc, http.MethodGet, base+"/unio/admin/v1/buckets", "", auth, "")
	if r.status != http.StatusOK || !strings.Contains(r.body, "bridge-bucket") {
		t.Errorf("list: status %d body %s", r.status, r.body)
	}
	if r.header.Get("Cache-Control") != "no-store" || r.header.Get("Content-Type") != "application/json" {
		t.Errorf("list headers: Cache-Control %q, Content-Type %q", r.header.Get("Cache-Control"), r.header.Get("Content-Type"))
	}
	if r.header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("list X-Content-Type-Options = %q", r.header.Get("X-Content-Type-Options"))
	}

	// A body over the limit is refused by the admin service.
	big := `{"name":"` + strings.Repeat("a", 70<<10) + `"}`
	r = doHost(t, hc, http.MethodPost, base+"/unio/admin/v1/buckets", "", with(map[string]string{
		"Content-Type": "application/json", "X-Unio-CSRF": session.CSRF,
	}), big)
	if r.status != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: status %d, want 413", r.status)
	}

	// Logout clears the client's cookie.
	r = doHost(t, hc, http.MethodDelete, base+"/unio/admin/v1/session", "", nil, "")
	if r.status != http.StatusNoContent || !strings.Contains(strings.Join(r.header.Values("Set-Cookie"), ""), "Max-Age=0") {
		t.Errorf("logout: status %d cookies %v", r.status, r.header.Values("Set-Cookie"))
	}
}

func TestAdminAPIIsNotReachedThroughBuckets(t *testing.T) {
	svc, _, _ := bridgeAdmin(t)
	base := startServerWith(t, s3api.Config{
		Admin:   svc,
		Domains: []string{"s3.unio.local"},
	}, nil, nil, nil)
	hc := newHTTPClient(t, nil)

	// A virtual-hosted request to the admin path is an object request on that
	// bucket. It must not reach the admin service, so its answer is never the
	// admin JSON.
	r := doHost(t, hc, http.MethodGet, base+"/unio/admin/v1/cluster", "photos.s3.unio.local", nil, "")
	if strings.Contains(r.body, `"requestId"`) || strings.Contains(r.body, `"clusterId"`) {
		t.Fatalf("the admin API answered a bucket request: %s", r.body)
	}
	if !strings.Contains(r.body, "<Error>") {
		t.Errorf("bucket request answer is not an S3 error: %s", r.body)
	}

	// The bare domain is the admin API's host, as on any path-style request.
	r = doHost(t, hc, http.MethodGet, base+"/unio/admin/v1/cluster", "s3.unio.local", nil, "")
	if r.status != http.StatusUnauthorized {
		t.Errorf("admin on the bare domain: status %d, want 401", r.status)
	}
}
