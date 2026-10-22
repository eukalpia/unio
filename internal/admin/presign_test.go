package admin

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// presignPath is the presign endpoint of bucket.
func presignPath(bucket string) string {
	return bucketsPath + "/" + bucket + "/presign"
}

// verifyURL runs a presigned URL through the IAM authenticator, as the S3
// front end would for a request with that method, and returns the identity it
// resolves to.
func verifyURL(t *testing.T, f *fixture, rawURL, method string) (auth.Identity, error) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse %q: %v", rawURL, err)
	}
	res, err := f.iam.Authenticate(context.Background(), &auth.Request{
		Method:   method,
		Host:     u.Host,
		RawPath:  u.EscapedPath(),
		RawQuery: u.RawQuery,
		Header:   http.Header{},
	})
	if err != nil {
		return auth.Identity{}, err
	}
	return res.Identity, nil
}

// presignedURL returns the URL of a successful presign request.
func presignedURL(t *testing.T, f *fixture, creds func() Request, body string) string {
	t.Helper()
	resp := f.serve(creds())
	expectStatus(t, resp, http.StatusOK)
	var out struct {
		URL string `json:"url"`
	}
	mustUnmarshal(t, resp.Body, &out)
	if out.URL == "" {
		t.Fatalf("presign %s returned no url", body)
	}
	return out.URL
}

func TestPresignSignsAsTheSessionUser(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)

	for _, method := range []string{"PUT", "GET"} {
		t.Run(method, func(t *testing.T) {
			body := `{"method":"` + method + `","key":"docs/report 1.txt","expiresSeconds":300}`
			raw := presignedURL(t, f, func() Request {
				return f.sigv4(creds, http.MethodPost, presignPath("photos"), "", []byte(body))
			}, body)

			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			if u.Scheme != "http" || u.Host != testHost {
				t.Errorf("url %s is not on the request host over http", raw)
			}
			if u.EscapedPath() != "/photos/docs/report%201.txt" {
				t.Errorf("path = %q, want the escaped key", u.EscapedPath())
			}
			q := u.Query()
			if q.Get("X-Amz-Expires") != "300" || q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" {
				t.Errorf("query = %s", u.RawQuery)
			}
			if !strings.HasPrefix(q.Get("X-Amz-Credential"), key+"/") {
				t.Errorf("credential %q does not name the session key", q.Get("X-Amz-Credential"))
			}
			if strings.Contains(raw, secret) {
				t.Error("the URL contains the secret")
			}

			id, err := verifyURL(t, f, raw, method)
			if err != nil {
				t.Fatalf("the URL does not verify for %s: %v", method, err)
			}
			if id.User != "alice" || id.AccessKey != key {
				t.Errorf("identity = %+v, want alice", id)
			}

			// The URL is bound to its method.
			other := "GET"
			if method == "GET" {
				other = "PUT"
			}
			if _, err := verifyURL(t, f, raw, other); err == nil {
				t.Errorf("the %s URL verifies as %s", method, other)
			} else if !s3err.Is(err, s3err.SignatureDoesNotMatch) {
				t.Errorf("method swap failed with %v, want SignatureDoesNotMatch", err)
			}
		})
	}
}

func TestPresignAsRootSignsWithTheRootSecret(t *testing.T) {
	f := newFixture(t)
	body := `{"method":"PUT","key":"k","expiresSeconds":60}`
	raw := presignedURL(t, f, func() Request {
		return f.sigv4(rootCreds(), http.MethodPost, presignPath("photos"), "", []byte(body))
	}, body)
	id, err := verifyURL(t, f, raw, "PUT")
	if err != nil {
		t.Fatalf("root URL does not verify: %v", err)
	}
	if !id.Root {
		t.Errorf("identity = %+v, want root", id)
	}
}

func TestPresignWithSessionCookieAndCSRF(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, csrf := f.login(key, secret)
	body := `{"method":"GET","key":"k","expiresSeconds":60}`
	raw := presignedURL(t, f, func() Request {
		return with(f.request(http.MethodPost, presignPath("photos"), body), withCookie(token), withCSRF(csrf))
	}, body)
	if _, err := verifyURL(t, f, raw, "GET"); err != nil {
		t.Fatalf("cookie presign does not verify: %v", err)
	}
}

// TestPresignAppliesIAM checks the user's own rights: a presigned URL for an
// action the session user may not perform is refused before anything is signed.
func TestPresignAppliesIAM(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("readonly", adminPolicy)
	body := `{"method":"PUT","key":"k","expiresSeconds":60}`
	expectError(t, f.serve(f.sigv4(credsOf(key, secret), http.MethodPost, presignPath("photos"), "", []byte(body))), http.StatusForbidden, "AccessDenied")
}

func TestPresignErrors(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	post := func(body string, host string) Response {
		t.Helper()
		req := f.sigv4(creds, http.MethodPost, presignPath("photos"), "", []byte(body))
		if host != "" {
			req.Host = host
		}
		return f.serve(req)
	}
	cases := []struct {
		name   string
		body   string
		host   string
		status int
		code   string
	}{
		{"method POST", `{"method":"POST","key":"k","expiresSeconds":60}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"method lower case", `{"method":"get","key":"k","expiresSeconds":60}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"method missing", `{"key":"k","expiresSeconds":60}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"expires zero", `{"method":"GET","key":"k","expiresSeconds":0}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"expires missing", `{"method":"GET","key":"k"}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"expires negative", `{"method":"GET","key":"k","expiresSeconds":-1}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"expires over an hour", `{"method":"GET","key":"k","expiresSeconds":3601}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"expires a string", `{"method":"GET","key":"k","expiresSeconds":"60"}`, "", http.StatusBadRequest, "InvalidRequest"},
		{"expires a fraction", `{"method":"GET","key":"k","expiresSeconds":1.5}`, "", http.StatusBadRequest, "InvalidRequest"},
		{"key missing", `{"method":"GET","expiresSeconds":60}`, "", http.StatusBadRequest, "InvalidArgument"},
		{"key overlong", `{"method":"GET","key":"` + longString(2000) + `","expiresSeconds":60}`, "", http.StatusBadRequest, "KeyTooLongError"},
		{"unknown field", `{"method":"GET","key":"k","expiresSeconds":60,"bucket":"x"}`, "", http.StatusBadRequest, "InvalidRequest"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectError(t, post(tc.body, tc.host), tc.status, tc.code)
		})
	}

	// The bucket is checked too.
	req := f.sigv4(creds, http.MethodPost, bucketsPath+"/Bad_Name/presign", "", []byte(`{"method":"GET","key":"k","expiresSeconds":60}`))
	expectError(t, f.serve(req), http.StatusBadRequest, "InvalidBucketName")
}

func TestPresignBoundsAreInclusive(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	for _, n := range []string{"1", "3600"} {
		body := `{"method":"GET","key":"k","expiresSeconds":` + n + `}`
		expectStatus(t, f.serve(f.sigv4(credsOf(key, secret), http.MethodPost, presignPath("photos"), "", []byte(body))), http.StatusOK)
	}
}

func TestPresignRequiresAJSONBody(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	expectError(t, f.serve(f.sigv4(credsOf(key, secret), http.MethodPost, presignPath("photos"), "", nil)), http.StatusBadRequest, "InvalidRequest")
}

func TestPresignWithoutCredentials(t *testing.T) {
	f := newFixture(t)
	req := f.request(http.MethodPost, presignPath("photos"), `{"method":"GET","key":"k","expiresSeconds":60}`)
	expectError(t, f.serve(req), http.StatusUnauthorized, "AccessDenied")
}

// TestPresignRefusesABadHost checks the Host header that a presigned URL is
// built on. A signed request cannot change its host, so this uses the session
// cookie, which carries no signature.
func TestPresignRefusesABadHost(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, csrf := f.login(key, secret)
	for _, host := range []string{"evil.example/x", "a b", "u@h", "[::1"} {
		req := with(f.request(http.MethodPost, presignPath("photos"), `{"method":"GET","key":"k","expiresSeconds":60}`),
			withCookie(token), withCSRF(csrf))
		req.Host = host
		expectError(t, f.serve(req), http.StatusBadRequest, "InvalidRequest")
	}
}
