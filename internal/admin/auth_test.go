package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// TestAuthMatrix runs one read endpoint under every kind of credential and
// checks the status and code of each answer.
func TestAuthMatrix(t *testing.T) {
	cluster := BasePath + "/cluster"
	cases := []struct {
		name   string
		build  func(t *testing.T, f *fixture) Request
		status int
		code   string
	}{
		{
			name:   "no auth",
			build:  func(t *testing.T, f *fixture) Request { return f.get(cluster) },
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "wrong SigV4 secret",
			build: func(t *testing.T, f *fixture) Request {
				key, _ := f.createUser("alice", fullPolicy)
				return f.sigv4(credsOf(key, "wrong-secret-0123456789-abcdefghijk"), http.MethodGet, cluster, "", nil)
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "unknown SigV4 key",
			build: func(t *testing.T, f *fixture) Request {
				return f.sigv4(credsOf("NOSUCHUSERKEY0001", testRootSecret), http.MethodGet, cluster, "", nil)
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "SigV4 non-admin",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("viewer", s3OnlyPolicy)
				return f.sigv4(credsOf(key, secret), http.MethodGet, cluster, "", nil)
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "SigV4 user without any policy",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("bare", "")
				return f.sigv4(credsOf(key, secret), http.MethodGet, cluster, "", nil)
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "SigV4 admin",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				return f.sigv4(credsOf(key, secret), http.MethodGet, cluster, "", nil)
			},
			status: http.StatusOK,
		},
		{
			name: "SigV4 root",
			build: func(t *testing.T, f *fixture) Request {
				return f.sigv4(rootCreds(), http.MethodGet, cluster, "", nil)
			},
			status: http.StatusOK,
		},
		{
			name: "valid cookie",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				return with(f.get(cluster), withCookie(token))
			},
			status: http.StatusOK,
		},
		{
			name: "expired cookie",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				f.clock.Advance(SessionTTL + time.Second)
				return with(f.get(cluster), withCookie(token))
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "tampered cookie payload",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				return with(f.get(cluster), withCookie(tamper(token)))
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "cookie with a signature from another key",
			build: func(t *testing.T, f *fixture) Request {
				key, _ := f.createUser("alice", fullPolicy)
				// A session sealed under another cluster's key is a valid-looking
				// token that must not verify here.
				other := newSessionSealer(t, "some-other-cluster-secret-0123456789abcd")
				forged, err := other.sealSession(sessionClaims{AccessKey: key, IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL)})
				if err != nil {
					t.Fatal(err)
				}
				return with(f.get(cluster), withCookie(forged))
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "cookie of a disabled user",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				if err := f.iam.SetUserEnabled(t.Context(), "alice", false); err != nil {
					t.Fatal(err)
				}
				return with(f.get(cluster), withCookie(token))
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "cookie of a user who lost the admin right",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				if err := f.iam.SetUserPolicy(t.Context(), "alice", []byte(s3OnlyPolicy)); err != nil {
					t.Fatal(err)
				}
				return with(f.get(cluster), withCookie(token))
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "cookie of a deleted user",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				if err := f.iam.DeleteUser(t.Context(), "alice"); err != nil {
					t.Fatal(err)
				}
				return with(f.get(cluster), withCookie(token))
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
		{
			name: "cookie with a signed request that fails",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				// A signed request wins over the cookie, so a wrong signature is not
				// rescued by a valid session.
				req := f.sigv4(credsOf(key, "wrong-secret-0123456789-abcdefghijk"), http.MethodGet, cluster, "", nil)
				return with(req, withCookie(token))
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "garbage authorization with a valid cookie",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				return with(f.get(cluster), withCookie(token), func(r *Request) {
					r.Header.Set("Authorization", "Bearer something")
				})
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "cookie without the cookie name",
			build: func(t *testing.T, f *fixture) Request {
				key, secret := f.createUser("alice", fullPolicy)
				token, _ := f.login(key, secret)
				return with(f.get(cluster), func(r *Request) {
					r.Header.Set("Cookie", "other="+token)
				})
			},
			status: http.StatusUnauthorized, code: "AccessDenied",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			resp := f.serve(tc.build(t, f))
			expectStatus(t, resp, tc.status)
			if tc.code != "" {
				expectError(t, resp, tc.status, tc.code)
			}
		})
	}
}

// TestCSRF covers the CSRF rule for a state-changing request authenticated by
// the cookie: it needs the token of its own session, exactly once.
func TestCSRF(t *testing.T) {
	createBucket := func(f *fixture, mods ...func(*Request)) Request {
		return with(f.request(http.MethodPost, BasePath+"/buckets", map[string]string{"name": "csrf-bucket"}), mods...)
	}
	cases := []struct {
		name   string
		build  func(f *fixture, token, csrf string) Request
		status int
		code   string
	}{
		{
			name: "missing token",
			build: func(f *fixture, token, _ string) Request {
				return createBucket(f, withCookie(token))
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "wrong token",
			build: func(f *fixture, token, _ string) Request {
				return createBucket(f, withCookie(token), withCSRF("not-the-token"))
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "empty token",
			build: func(f *fixture, token, _ string) Request {
				return createBucket(f, withCookie(token), withCSRF(""))
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "token of another session",
			build: func(f *fixture, token, _ string) Request {
				key, secret := f.createUser("bob", fullPolicy)
				_, otherCSRF := f.login(key, secret)
				return createBucket(f, withCookie(token), withCSRF(otherCSRF))
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "token given twice",
			build: func(f *fixture, token, csrf string) Request {
				req := createBucket(f, withCookie(token), withCSRF(csrf))
				req.Header.Add(CSRFHeader, csrf)
				return req
			},
			status: http.StatusForbidden, code: "AccessDenied",
		},
		{
			name: "correct token",
			build: func(f *fixture, token, csrf string) Request {
				return createBucket(f, withCookie(token), withCSRF(csrf))
			},
			status: http.StatusCreated,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			key, secret := f.createUser("alice", fullPolicy)
			token, csrf := f.login(key, secret)
			resp := f.serve(tc.build(f, token, csrf))
			expectStatus(t, resp, tc.status)
			if tc.code != "" {
				expectError(t, resp, tc.status, tc.code)
			}
		})
	}
}

// TestCSRFIgnoredOnReads checks that a read does not need the token, and that a
// wrong token on a read is ignored.
func TestCSRFIgnoredOnReads(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, _ := f.login(key, secret)
	expectStatus(t, f.serve(with(f.get(BasePath+"/cluster"), withCookie(token))), http.StatusOK)
	expectStatus(t, f.serve(with(f.get(BasePath+"/cluster"), withCookie(token), withCSRF("bogus"))), http.StatusOK)
	expectStatus(t, f.serve(with(f.get(BasePath+"/buckets"), withCookie(token), withCSRF(""))), http.StatusOK)
}

// TestCSRFNotNeededForSigV4 checks that a signed mutation needs no CSRF token,
// because a browser cannot attach a signature to a cross-site request.
func TestCSRFNotNeededForSigV4(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	body := []byte(`{"name":"signed-bucket"}`)
	expectStatus(t, f.serve(f.sigv4(credsOf(key, secret), http.MethodPost, BasePath+"/buckets", "", body)), http.StatusCreated)
}

// tamper returns token with one payload byte changed, so its HMAC no longer
// matches.
func tamper(token string) string {
	b := []byte(token)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}

// newSessionSealer returns a service whose session key comes from another
// cluster secret, so that the tokens it seals do not verify on the test service.
func newSessionSealer(t *testing.T, secret string) *Service {
	t.Helper()
	key, err := deriveSessionKey([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return &Service{key: key}
}

// TestPresignedAdminRequests checks a request signed through its query, as a
// presigned URL carries it. Such a request authenticates, and its body is not
// covered by the signature, so a body is refused.
func TestPresignedAdminRequests(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)

	get := f.presignedRequest(creds, http.MethodGet, BasePath+"/cluster")
	expectStatus(t, f.serve(get), http.StatusOK)

	withBody := f.presignedRequest(creds, http.MethodPost, BasePath+"/buckets")
	withBody.Body = strings.NewReader(`{"name":"unsigned-body"}`)
	withBody.ContentLength = int64(len(`{"name":"unsigned-body"}`))
	withBody.Header.Set("Content-Type", "application/json")
	expectError(t, f.serve(withBody), http.StatusForbidden, "AccessDenied")
	if _, err := f.layer.GetBucketInfo(t.Context(), "unsigned-body"); err == nil {
		t.Error("the bucket of an unsigned body was created")
	}

	// An empty body on a presigned request is allowed past authentication.
	empty := f.presignedRequest(creds, http.MethodPost, BasePath+"/buckets")
	expectError(t, f.serve(empty), http.StatusBadRequest, "InvalidRequest")
}

// presignedRequest returns a request whose authorization is in its query, built
// with the SDK's presign, as a client would send a presigned URL.
func (f *fixture) presignedRequest(creds aws.Credentials, method, path string) Request {
	f.t.Helper()
	u, err := url.Parse("http://" + testHost + path)
	if err != nil {
		f.t.Fatal(err)
	}
	q := u.Query()
	q.Set("X-Amz-Expires", "300")
	u.RawQuery = q.Encode()
	hreq, err := http.NewRequestWithContext(context.Background(), method, u.String(), nil)
	if err != nil {
		f.t.Fatal(err)
	}
	signed, _, err := v4.NewSigner().PresignHTTP(context.Background(), creds, hreq, unsignedPayload, "s3", testRegion, f.clock.Now(),
		func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	if err != nil {
		f.t.Fatalf("presign: %v", err)
	}
	su, err := url.Parse(signed)
	if err != nil {
		f.t.Fatal(err)
	}
	return Request{
		Method:        method,
		Path:          su.EscapedPath(),
		Query:         su.RawQuery,
		Host:          testHost,
		Header:        http.Header{},
		ContentLength: -1,
		RemoteAddr:    testRemote,
		RequestID:     "req-presigned",
	}
}
