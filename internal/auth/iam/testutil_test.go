package iam

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/meta"
)

const (
	testRootKey    = "ROOTKEY00000001234"
	testRootSecret = "rootsecret-0123456789-abcdefghijklmnopqrstuvwxyz"
	testRegion     = "us-east-1"
)

var testClusterSecret = []byte("test cluster secret for unit tests only")

// fakeClock is a settable clock safe for concurrent use.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// Now returns the current fake time.
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake time forward.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newClock returns a fake clock at a fixed instant.
func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
}

// testConfig returns a valid Config bound to clock.
func testConfig(clock *fakeClock) Config {
	return Config{
		KEK:           DeriveKEK(testClusterSecret),
		RootAccessKey: testRootKey,
		RootSecret:    testRootSecret,
		Region:        testRegion,
		Clock:         clock.Now,
		CacheTTL:      10 * time.Second,
		CacheSize:     64,
		MaxRetries:    8,
	}
}

// newTestStore returns a Store over a fresh MemKV and the clock it uses.
func newTestStore(t testing.TB) (*Store, *authtest.MemKV, *fakeClock) {
	t.Helper()
	kv := authtest.NewMemKV()
	clock := newClock()
	s, err := New(kv, testConfig(clock))
	if err != nil {
		t.Fatal(err)
	}
	return s, kv, clock
}

// signRequest returns a GET request signed with the given credentials at
// when. It fails the test on error.
func signRequest(t testing.TB, accessKey, secret string, when time.Time) *auth.Request {
	t.Helper()
	req, err := signRequestE(accessKey, secret, when)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// signRequestE is signRequest for callers that cannot call t.Fatal, such as
// goroutines. The request targets bucket "photos", key "a.jpg".
func signRequestE(accessKey, secret string, when time.Time) (*auth.Request, error) {
	r, err := http.NewRequest(http.MethodGet, "https://photos.s3.example.com/a.jpg", nil)
	if err != nil {
		return nil, err
	}
	r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
	signer := v4.NewSigner(func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	creds := aws.Credentials{AccessKeyID: accessKey, SecretAccessKey: secret}
	if err := signer.SignHTTP(context.Background(), creds, r, "UNSIGNED-PAYLOAD", "s3", testRegion, when); err != nil {
		return nil, err
	}
	return &auth.Request{
		Method: r.Method, Host: r.Host, RawPath: r.URL.EscapedPath(),
		Header: r.Header.Clone(), ContentLength: 0,
	}, nil
}

// faultyKV wraps a SystemKV and fails the first failPuts writes whose key
// has the given prefix with ErrRevisionMismatch, to exercise retries.
type faultyKV struct {
	meta.SystemKV
	mu       sync.Mutex
	prefix   string
	failPuts int
}

// Put implements meta.SystemKV.
func (f *faultyKV) Put(ctx context.Context, key string, value []byte, expect uint64) (uint64, error) {
	f.mu.Lock()
	if f.failPuts > 0 && len(key) >= len(f.prefix) && key[:len(f.prefix)] == f.prefix {
		f.failPuts--
		f.mu.Unlock()
		return 0, meta.ErrRevisionMismatch
	}
	f.mu.Unlock()
	return f.SystemKV.Put(ctx, key, value, expect)
}

// mustCreateUser creates a user or fails the test.
func mustCreateUser(t testing.TB, s *Store, name string) {
	t.Helper()
	if _, err := s.CreateUser(context.Background(), name); err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
}

// mustCreateKey creates an access key or fails the test.
func mustCreateKey(t testing.TB, s *Store, user string) auth.AccessKey {
	t.Helper()
	k, err := s.CreateAccessKey(context.Background(), user)
	if err != nil {
		t.Fatalf("create key for %s: %v", user, err)
	}
	return k
}

// readAllowPolicy grants read access to photos and denies deletes.
const readAllowPolicy = `{"Version":"2012-10-17","Statement":[` +
	`{"Effect":"Allow","Action":["s3:GetObject","s3:ListBucket"],"Resource":["arn:aws:s3:::photos","arn:aws:s3:::photos/*"]},` +
	`{"Effect":"Deny","Action":"s3:DeleteObject","Resource":"*"}]}`
