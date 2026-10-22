package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/clustertest"
	"github.com/eukalpia/unio/internal/cluster/topology"
	"github.com/eukalpia/unio/internal/drive/drivetest"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
)

// Test credentials. They satisfy the root-credential rules and exist only in
// tests.
const (
	testRoot       = "UNIOADMINTESTROOT"
	testRootSecret = "root-secret-for-admin-tests-0123456789-ABCDEFGH"
	testSecret     = "cluster-secret-for-admin-tests-0123456789abcdef"
	testHost       = "unio.test:9000"
	testRemote     = "192.0.2.10:40000"
	testRegion     = "us-east-1"
	// adminPolicy grants the admin right and nothing else.
	adminPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"unio:Admin","Resource":"*"}]}`
	// fullPolicy grants the admin right and every S3 action.
	fullPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["unio:Admin","s3:*"],"Resource":"*"}]}`
	// s3OnlyPolicy grants S3 but not the admin right.
	s3OnlyPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`
)

// testNow is the fixed time of every test.
var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// fakeClock is a clock that tests move by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the clock reading.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeDeletions reports a fixed backlog.
type fakeDeletions struct{ pending int }

// Stats implements Deletions.
func (f fakeDeletions) Stats() engine.Stats {
	return engine.Stats{GCPending: f.pending}
}

// fixture is one service with its components, all in memory.
type fixture struct {
	t     *testing.T
	svc   *Service
	iam   *iam.Store
	layer *objecttest.Layer
	topo  *clustertest.FakeTopology
	rec   *Recorder
	clock *fakeClock
	m     *cluster.Map
}

// newFixture builds a service over a real IAM store, an in-memory object layer
// and a two-node cluster in which only the local node's drives answer. opts
// may change the configuration before the service is built.
func newFixture(t *testing.T, opts ...func(*Config)) *fixture {
	t.Helper()
	clock := &fakeClock{now: testNow}
	store, err := iam.New(authtest.NewMemKV(), iam.Config{
		KEK:           iam.DeriveKEK([]byte(testSecret)),
		RootAccessKey: testRoot,
		RootSecret:    testRootSecret,
		Region:        testRegion,
		Clock:         clock.Now,
	})
	if err != nil {
		t.Fatalf("iam: %v", err)
	}
	m, err := topology.MapFromSpec(topology.Spec{
		ClusterID: "0123456789abcdef0123456789abcdef",
		Nodes: []topology.NodeSpec{
			{ID: "node-a", Addr: "10.0.0.1:9100", S3Addr: "10.0.0.1:9000", Drives: []string{"/data/a1", "/data/a2"}},
			{ID: "node-b", Addr: "10.0.0.2:9100", S3Addr: "10.0.0.2:9000", Drives: []string{"/data/b1", "/data/b2"}},
		},
		SetSize:    4,
		Parity:     2,
		Partitions: 2,
	})
	if err != nil {
		t.Fatalf("cluster map: %v", err)
	}
	topo := clustertest.NewFakeTopology("node-a", m)
	for _, d := range m.Drives {
		if d.Node == "node-a" {
			topo.SetDrive(drivetest.NewMemory(d.ID))
		}
	}
	f := &fixture{
		t:     t,
		iam:   store,
		layer: objecttest.NewLayer(),
		topo:  topo,
		rec:   NewRecorder(clock.Now),
		clock: clock,
		m:     m,
	}
	cfg := Config{
		IAM:           store,
		Credentials:   store,
		Layer:         f.layer,
		Cluster:       topo,
		Deletions:     fakeDeletions{pending: 3},
		Recorder:      f.rec,
		BufferInUse:   func() int64 { return 4096 },
		RootAccessKey: testRoot,
		SessionSecret: []byte(testSecret),
		Region:        testRegion,
		LoginAttempts: 1000,
		Clock:         clock.Now,
		Logger:        slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(&cfg)
	}
	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f.svc = svc
	return f
}

// createUser makes an enabled user with one access key and the given policy
// document, which may be empty. It returns the access key ID and secret.
func (f *fixture) createUser(name, policy string) (string, string) {
	f.t.Helper()
	ctx := context.Background()
	if _, err := f.iam.CreateUser(ctx, name); err != nil {
		f.t.Fatalf("create user %s: %v", name, err)
	}
	key, err := f.iam.CreateAccessKey(ctx, name)
	if err != nil {
		f.t.Fatalf("create key for %s: %v", name, err)
	}
	if policy != "" {
		if err := f.iam.SetUserPolicy(ctx, name, []byte(policy)); err != nil {
			f.t.Fatalf("set policy for %s: %v", name, err)
		}
	}
	return key.ID, key.Secret
}

// request builds an unauthenticated admin request. A non-nil body is marshalled
// to JSON; a []byte or string is sent as it is, and a body is declared as
// application/json.
func (f *fixture) request(method, path string, body any) Request {
	f.t.Helper()
	req := Request{
		Method:        method,
		Path:          path,
		Host:          testHost,
		Header:        http.Header{},
		ContentLength: -1,
		RemoteAddr:    testRemote,
		RequestID:     "req-test",
	}
	if body != nil {
		var raw []byte
		switch v := body.(type) {
		case []byte:
			raw = v
		case string:
			raw = []byte(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				f.t.Fatalf("marshal body: %v", err)
			}
			raw = b
		}
		req.Body = bytes.NewReader(raw)
		req.ContentLength = int64(len(raw))
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// get is request for GET with no body.
func (f *fixture) get(path string) Request { return f.request(http.MethodGet, path, nil) }

// serve sends req to the service and returns the response.
func (f *fixture) serve(req Request) Response {
	f.t.Helper()
	return f.svc.Serve(context.Background(), req)
}

// withCookie attaches the session cookie to req.
func withCookie(token string) func(*Request) {
	return func(r *Request) {
		r.Header.Set("Cookie", SessionCookie+"="+token)
	}
}

// withCSRF attaches the CSRF header to req.
func withCSRF(token string) func(*Request) {
	return func(r *Request) {
		r.Header.Set(CSRFHeader, token)
	}
}

// with applies every modifier to req and returns it.
func with(req Request, mods ...func(*Request)) Request {
	for _, m := range mods {
		m(&req)
	}
	return req
}

// login logs in with key and secret and returns the session cookie value and
// the CSRF token. It fails the test unless the login succeeds.
func (f *fixture) login(key, secret string) (string, string) {
	f.t.Helper()
	resp := f.serve(f.request(http.MethodPost, BasePath+"/session", map[string]string{
		"accessKey": key,
		"secretKey": secret,
	}))
	if resp.Status != http.StatusOK {
		f.t.Fatalf("login %s: status %d: %s", key, resp.Status, resp.Body)
	}
	var out struct {
		CSRF string `json:"csrf"`
	}
	mustUnmarshal(f.t, resp.Body, &out)
	return cookieValue(f.t, resp.Header), out.CSRF
}

// cookieValue returns the value of the session cookie in a Set-Cookie header.
func cookieValue(t *testing.T, h http.Header) string {
	t.Helper()
	r := &http.Response{Header: http.Header{"Set-Cookie": h.Values("Set-Cookie")}}
	for _, c := range r.Cookies() {
		if c.Name == SessionCookie {
			return c.Value
		}
	}
	t.Fatalf("no %s cookie in %v", SessionCookie, h.Values("Set-Cookie"))
	return ""
}

// sigv4 returns a request signed with creds, for method and path with query
// and body. The payload hash covers the body.
func (f *fixture) sigv4(creds aws.Credentials, method, path, query string, body []byte) Request {
	f.t.Helper()
	// path is already escaped, as a client sends it, so it is parsed rather than
	// set as the decoded path.
	u, err := url.Parse("http://" + testHost + path)
	if err != nil {
		f.t.Fatalf("path %q: %v", path, err)
	}
	u.RawQuery = query
	hreq, err := http.NewRequestWithContext(context.Background(), method, u.String(), bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	if len(body) > 0 {
		hreq.Header.Set("Content-Type", "application/json")
	}
	sum := sha256.Sum256(body)
	payload := hex.EncodeToString(sum[:])
	hreq.Header.Set("X-Amz-Content-Sha256", payload)
	// The path is escaped once by the URL, and the signer must use that escaping
	// as it is, as the verifier does with the path that arrives.
	if err := v4.NewSigner().SignHTTP(context.Background(), creds, hreq, payload, "s3", testRegion, f.clock.Now(),
		func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true }); err != nil {
		f.t.Fatalf("sign: %v", err)
	}
	return Request{
		Method:        method,
		Path:          u.EscapedPath(),
		Query:         query,
		Host:          testHost,
		Header:        hreq.Header,
		Body:          bytes.NewReader(body),
		ContentLength: int64(len(body)),
		RemoteAddr:    testRemote,
		RequestID:     "req-sigv4",
	}
}

// rootCreds are the credentials of the root account.
func rootCreds() aws.Credentials {
	return aws.Credentials{AccessKeyID: testRoot, SecretAccessKey: testRootSecret}
}

// credsOf returns the credentials of an access key.
func credsOf(key, secret string) aws.Credentials {
	return aws.Credentials{AccessKeyID: key, SecretAccessKey: secret}
}

// mustUnmarshal decodes a JSON body into v and fails the test on error.
func mustUnmarshal(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

// errorCode returns the code and message of an error response.
func errorCode(t *testing.T, resp Response) (string, string) {
	t.Helper()
	var e errorBody
	mustUnmarshal(t, resp.Body, &e)
	if e.RequestID == "" {
		t.Errorf("error response carries no request id: %s", resp.Body)
	}
	return e.Code, e.Message
}

// expectError fails the test unless resp is an error with the status and code.
func expectError(t *testing.T, resp Response, status int, code string) {
	t.Helper()
	if resp.Status != status {
		t.Fatalf("status = %d, want %d (body %s)", resp.Status, status, resp.Body)
	}
	if got, _ := errorCode(t, resp); got != code {
		t.Fatalf("code = %q, want %q (body %s)", got, code, resp.Body)
	}
}

// expectStatus fails the test unless resp has the status.
func expectStatus(t *testing.T, resp Response, status int) {
	t.Helper()
	if resp.Status != status {
		t.Fatalf("status = %d, want %d (body %s)", resp.Status, status, resp.Body)
	}
}

// expectNoStore fails the test unless resp forbids caching.
func expectNoStore(t *testing.T, resp Response) {
	t.Helper()
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// itoa is strconv.Itoa for the test tables.
func itoa(n int) string { return strconv.Itoa(n) }

// bucketOpts returns the options of a bucket created by a test.
func bucketOpts() object.MakeBucketOptions {
	return object.MakeBucketOptions{Owner: "test"}
}

// getOpts returns the options of an object read by a test.
func getOpts() object.GetOptions {
	return object.GetOptions{}
}

// putOpts returns the options of an object written by a test.
func putOpts() object.PutOptions {
	return object.PutOptions{}
}

// newBody wraps data as the body of an object write.
func newBody(data []byte) object.Body {
	return objecttest.NewBody(data)
}
