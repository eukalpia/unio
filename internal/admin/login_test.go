package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLoginSuccessSetsCookieAndReturnsCSRF(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)

	resp := f.serve(f.request(http.MethodPost, BasePath+"/session", map[string]string{
		"accessKey": key, "secretKey": secret,
	}))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)

	var out struct {
		AccessKey string `json:"accessKey"`
		ExpiresAt string `json:"expiresAt"`
		CSRF      string `json:"csrf"`
	}
	mustUnmarshal(t, resp.Body, &out)
	if out.AccessKey != key {
		t.Errorf("accessKey = %q, want %q", out.AccessKey, key)
	}
	if want := testNow.Add(SessionTTL).Format(time.RFC3339); out.ExpiresAt != want {
		t.Errorf("expiresAt = %q, want %q", out.ExpiresAt, want)
	}
	if out.CSRF == "" {
		t.Error("csrf is empty")
	}

	cookie := strings.Join(resp.Header.Values("Set-Cookie"), "")
	for _, want := range []string{"unio_session=", "Path=/unio/", "HttpOnly", "SameSite=Strict", "Max-Age=43200"} {
		if !strings.Contains(cookie, want) {
			t.Errorf("Set-Cookie %q lacks %q", cookie, want)
		}
	}
	if strings.Contains(cookie, "Secure") {
		t.Errorf("Set-Cookie %q is Secure without TLS", cookie)
	}
	if strings.Contains(string(resp.Body), secret) {
		t.Error("the login response contains the secret")
	}
}

func TestLoginSecureCookieWhenTLS(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Secure = true })
	key, secret := f.createUser("alice", fullPolicy)
	resp := f.serve(f.request(http.MethodPost, BasePath+"/session", map[string]string{
		"accessKey": key, "secretKey": secret,
	}))
	expectStatus(t, resp, http.StatusOK)
	if cookie := strings.Join(resp.Header.Values("Set-Cookie"), ""); !strings.Contains(cookie, "Secure") {
		t.Errorf("Set-Cookie %q lacks Secure under TLS", cookie)
	}
}

func TestLoginRefusalsAreOneGenericDenial(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	vkey, vsecret := f.createUser("viewer", s3OnlyPolicy)
	dkey, dsecret := f.createUser("disabled", fullPolicy)
	if err := f.iam.SetUserEnabled(t.Context(), "disabled", false); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		access string
		secret string
	}{
		{"wrong secret", key, secret + "x"},
		{"unknown key", "UNKNOWNUSERKEY001", secret},
		{"empty secret", key, ""},
		{"missing admin right", vkey, vsecret},
		{"disabled user", dkey, dsecret},
		{"empty access key", "", secret},
		{"oversized access key", strings.Repeat("A", 200), secret},
		{"oversized secret", key, strings.Repeat("s", 300)},
		{"root with a wrong secret", testRoot, "root-wrong-secret-0123456789-abcdef"},
	}
	var first string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := f.serve(f.request(http.MethodPost, BasePath+"/session", map[string]string{
				"accessKey": tc.access, "secretKey": tc.secret,
			}))
			expectError(t, resp, http.StatusForbidden, "AccessDenied")
			if strings.Contains(string(resp.Body), secret) || strings.Contains(string(resp.Body), dsecret) {
				t.Error("the denial contains a secret")
			}
			if len(resp.Header.Values("Set-Cookie")) != 0 {
				t.Error("a refused login sets a cookie")
			}
			// Every refusal carries the same body except for the request id.
			var e errorBody
			mustUnmarshal(t, resp.Body, &e)
			e.RequestID = ""
			b, _ := json.Marshal(e)
			if first == "" {
				first = string(b)
			} else if string(b) != first {
				t.Errorf("refusal body %s differs from the first refusal %s", b, first)
			}
		})
	}
}

func TestLoginRootAccount(t *testing.T) {
	f := newFixture(t)
	resp := f.serve(f.request(http.MethodPost, BasePath+"/session", map[string]string{
		"accessKey": testRoot, "secretKey": testRootSecret,
	}))
	expectStatus(t, resp, http.StatusOK)
	var out struct {
		AccessKey string `json:"accessKey"`
	}
	mustUnmarshal(t, resp.Body, &out)
	if out.AccessKey != testRoot {
		t.Errorf("accessKey = %q, want the root key", out.AccessKey)
	}
}

func TestLoginBodyErrors(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		body   any
		ctype  string
		status int
		code   string
	}{
		{"not json", "{not json", "", http.StatusBadRequest, "InvalidRequest"},
		{"unknown field", `{"accessKey":"x","secretKey":"y","extra":1}`, "", http.StatusBadRequest, "InvalidRequest"},
		{"two values", `{"accessKey":"x","secretKey":"y"} {}`, "", http.StatusBadRequest, "InvalidRequest"},
		{"empty body", "", "", http.StatusBadRequest, "InvalidRequest"},
		{"wrong content type", `{"accessKey":"x","secretKey":"y"}`, "text/plain", http.StatusUnsupportedMediaType, "InvalidRequest"},
		{"too large", strings.Repeat("a", MaxBodyBytes+1), "", http.StatusRequestEntityTooLarge, "EntityTooLarge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := f.request(http.MethodPost, BasePath+"/session", tc.body)
			if tc.ctype != "" {
				req.Header.Set("Content-Type", tc.ctype)
			}
			resp := f.serve(req)
			expectError(t, resp, tc.status, tc.code)
		})
	}
}

func TestLoginRateLimitAnswersSlowDown(t *testing.T) {
	// The default limit: ten attempts a minute per client.
	f := newFixture(t, func(c *Config) { c.LoginAttempts = 0 })
	f.createUser("alice", fullPolicy)
	bad := map[string]string{"accessKey": "UNKNOWNUSERKEY001", "secretKey": "wrong-wrong-wrong-wrong-wrong-wrong"}
	for i := 0; i < 10; i++ {
		expectError(t, f.serve(f.request(http.MethodPost, BasePath+"/session", bad)), http.StatusForbidden, "AccessDenied")
	}
	resp := f.serve(f.request(http.MethodPost, BasePath+"/session", bad))
	expectError(t, resp, http.StatusTooManyRequests, "SlowDown")
	if got := resp.Header.Get("Retry-After"); got != "6" {
		t.Errorf("Retry-After = %q, want 6 (one token every 6 seconds)", got)
	}

	// Another client is not affected.
	other := f.request(http.MethodPost, BasePath+"/session", bad)
	other.RemoteAddr = "198.51.100.7:1234"
	expectError(t, f.serve(other), http.StatusForbidden, "AccessDenied")

	// The first client gets one attempt back once the refill interval has passed.
	f.clock.Advance(7 * time.Second)
	expectError(t, f.serve(f.request(http.MethodPost, BasePath+"/session", bad)), http.StatusForbidden, "AccessDenied")
}

func TestLoginRateLimitCountsValidAttempts(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.LoginAttempts = 2 })
	key, secret := f.createUser("alice", fullPolicy)
	good := map[string]string{"accessKey": key, "secretKey": secret}
	expectStatus(t, f.serve(f.request(http.MethodPost, BasePath+"/session", good)), http.StatusOK)
	expectStatus(t, f.serve(f.request(http.MethodPost, BasePath+"/session", good)), http.StatusOK)
	expectError(t, f.serve(f.request(http.MethodPost, BasePath+"/session", good)), http.StatusTooManyRequests, "SlowDown")
}

// sessionBody is the decoded body of GET session.
type sessionBody struct {
	AccessKey string  `json:"accessKey"`
	ExpiresAt *string `json:"expiresAt"`
	CSRF      *string `json:"csrf"`
}

func TestGetSession(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, csrf := f.login(key, secret)

	resp := f.serve(with(f.get(BasePath+"/session"), withCookie(token)))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	var out sessionBody
	mustUnmarshal(t, resp.Body, &out)
	if out.AccessKey != key || out.ExpiresAt == nil {
		t.Fatalf("body %s: want accessKey %q and an expiry", resp.Body, key)
	}
	if out.CSRF == nil || *out.CSRF != csrf {
		t.Fatalf("csrf = %v, want the token the login returned", out.CSRF)
	}
}

// TestGetSessionCSRFResumesAfterReload checks that the token from GET session
// is accepted on a following state change, which is what a reloaded console
// needs. The login is not repeated.
func TestGetSessionCSRFResumesAfterReload(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, _ := f.login(key, secret)

	resp := f.serve(with(f.get(BasePath+"/session"), withCookie(token)))
	expectStatus(t, resp, http.StatusOK)
	var out sessionBody
	mustUnmarshal(t, resp.Body, &out)
	if out.CSRF == nil {
		t.Fatalf("no csrf in %s", resp.Body)
	}

	create := with(f.request(http.MethodPost, BasePath+"/buckets", map[string]string{"name": "resumed"}),
		withCookie(token), withCSRF(*out.CSRF))
	expectStatus(t, f.serve(create), http.StatusCreated)
}

func TestGetSessionWithSigV4HasNullExpiryAndCSRF(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	resp := f.serve(f.sigv4(credsOf(key, secret), http.MethodGet, BasePath+"/session", "", nil))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	var out map[string]any
	mustUnmarshal(t, resp.Body, &out)
	if v, ok := out["expiresAt"]; !ok || v != nil {
		t.Errorf("expiresAt = %v (present %v), want null for a signed caller", v, ok)
	}
	if v, ok := out["csrf"]; !ok || v != nil {
		t.Errorf("csrf = %v (present %v), want null for a signed caller", v, ok)
	}
	if out["accessKey"] != key {
		t.Errorf("accessKey = %v", out["accessKey"])
	}
}

// TestGetSessionRefusesStaleCookiesWithoutCSRF checks that an expired or
// tampered cookie gets 401, and that the body carries no CSRF token.
func TestGetSessionRefusesStaleCookiesWithoutCSRF(t *testing.T) {
	cases := []struct {
		name  string
		build func(f *fixture, token string) Request
	}{
		{"expired", func(f *fixture, token string) Request {
			f.clock.Advance(SessionTTL + time.Second)
			return with(f.get(BasePath+"/session"), withCookie(token))
		}},
		{"tampered", func(f *fixture, token string) Request {
			return with(f.get(BasePath+"/session"), withCookie(tamper(token)))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			key, secret := f.createUser("alice", fullPolicy)
			token, _ := f.login(key, secret)
			resp := f.serve(tc.build(f, token))
			expectError(t, resp, http.StatusUnauthorized, "AccessDenied")
			if strings.Contains(string(resp.Body), "csrf") {
				t.Errorf("the refusal carries a csrf field: %s", resp.Body)
			}
			expectNoStore(t, resp)
		})
	}
}

func TestGetSessionWithoutCredentials(t *testing.T) {
	f := newFixture(t)
	expectError(t, f.serve(f.get(BasePath+"/session")), http.StatusUnauthorized, "AccessDenied")
}

func TestDeleteSessionClearsCookieWithoutCredentials(t *testing.T) {
	f := newFixture(t)
	resp := f.serve(f.request(http.MethodDelete, BasePath+"/session", nil))
	expectStatus(t, resp, http.StatusNoContent)
	cookie := strings.Join(resp.Header.Values("Set-Cookie"), "")
	if !strings.Contains(cookie, "unio_session=") || !strings.Contains(cookie, "Max-Age=0") {
		t.Errorf("Set-Cookie %q does not clear the session", cookie)
	}
	if len(resp.Body) != 0 {
		t.Errorf("204 with a body: %q", resp.Body)
	}
}

func TestDeleteSessionEndsTheCookie(t *testing.T) {
	// The server keeps no session state, so a logout only clears the client's
	// cookie. The old token still verifies until it expires; this test pins
	// that the logout itself does not depend on it.
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	token, _ := f.login(key, secret)
	expectStatus(t, f.serve(f.request(http.MethodDelete, BasePath+"/session", nil)), http.StatusNoContent)
	expectStatus(t, f.serve(with(f.get(BasePath+"/session"), withCookie(token))), http.StatusOK)
}

func TestSessionEndpointMethods(t *testing.T) {
	f := newFixture(t)
	expectError(t, f.serve(f.request(http.MethodPut, BasePath+"/session", map[string]string{})), http.StatusMethodNotAllowed, "MethodNotAllowed")
	expectError(t, f.serve(f.request(http.MethodPatch, BasePath+"/session", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
}
