package admin

import (
	"bytes"
	"crypto/hkdf"
	"crypto/sha256"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestSessionKeyIsHKDFOfTheClusterSecret pins the derivation: the session key
// is HKDF-SHA256 of the cluster secret with the info string of the contract.
func TestSessionKeyIsHKDFOfTheClusterSecret(t *testing.T) {
	got, err := deriveSessionKey([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	want, err := hkdf.Key(sha256.New, []byte(testSecret), nil, "unio/v1/console-session", 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("session key is not HKDF(secret, info \"unio/v1/console-session\")")
	}
	if _, err := deriveSessionKey([]byte("too short")); err == nil {
		t.Error("a short cluster secret was accepted")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	s := newTestService(t)
	claims := sessionClaims{AccessKey: "ALICEKEY0000001", IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL)}
	token, err := s.sealSession(claims)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.openSession(token, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("openSession: %v", err)
	}
	if got.AccessKey != claims.AccessKey || !got.ExpiresAt.Equal(claims.ExpiresAt) || !got.IssuedAt.Equal(claims.IssuedAt) {
		t.Fatalf("claims = %+v, want %+v", got, claims)
	}
}

func TestOpenSessionRejections(t *testing.T) {
	s := newTestService(t)
	good := sessionClaims{AccessKey: "ALICEKEY0000001", IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL)}
	token, err := s.sealSession(good)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(token, ".", 2)
	payload, sig := parts[0], parts[1]

	// A payload with an extra field, sealed correctly, must still be refused.
	extra := []byte(`{"accessKey":"ALICEKEY0000001","issuedAt":"2026-10-22T12:00:00Z","expiresAt":"2026-10-23T00:00:00Z","role":"admin"}`)
	extraToken := sessionEncoding.EncodeToString(extra) + "." + sessionEncoding.EncodeToString(s.tag(extra))

	// A payload that outlives its twelve hours is refused even when signed.
	long := sessionClaims{AccessKey: "ALICEKEY0000001", IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL + time.Hour)}
	longToken, err := s.sealSession(long)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		token string
		at    time.Time
	}{
		{"empty", "", testNow},
		{"no dot", strings.ReplaceAll(token, ".", ""), testNow},
		{"two dots", token + ".x", testNow},
		{"overlong", strings.Repeat("a", maxSessionToken+1), testNow},
		{"bad base64 payload", "!!!." + sig, testNow},
		{"bad base64 signature", payload + ".!!!", testNow},
		{"padded base64", payload + "==." + sig, testNow},
		{"signature changed", payload + "." + sessionEncoding.EncodeToString(s.tag([]byte("other"))), testNow},
		{"payload changed", sessionEncoding.EncodeToString([]byte(`{"accessKey":"ZZZZZZZZZZZZZZZZ"}`)) + "." + sig, testNow},
		{"extra field under a valid tag", extraToken, testNow},
		{"expired", token, testNow.Add(SessionTTL)},
		{"long after expiry", token, testNow.Add(48 * time.Hour)},
		{"lifetime above twelve hours", longToken, testNow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.openSession(tc.token, tc.at); err == nil {
				t.Fatal("token accepted")
			}
		})
	}

	if _, err := s.openSession(token, testNow.Add(SessionTTL-time.Second)); err != nil {
		t.Errorf("a token one second before expiry is refused: %v", err)
	}
}

func TestSessionFromAnotherKeyIsRefused(t *testing.T) {
	s := newTestService(t)
	other := newSessionSealer(t, "another-cluster-secret-0123456789abcdef")
	token, err := other.sealSession(sessionClaims{AccessKey: "ALICEKEY0000001", IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.openSession(token, testNow); err == nil {
		t.Fatal("a session sealed under another key was accepted")
	}
}

func TestCSRFTokenBindsToTheCookie(t *testing.T) {
	s := newTestService(t)
	token, err := s.sealSession(sessionClaims{AccessKey: "ALICEKEY0000001", IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL)})
	if err != nil {
		t.Fatal(err)
	}
	csrf := s.csrfToken(token)
	if !s.csrfMatches(csrf, token) {
		t.Fatal("the CSRF token of a cookie does not match it")
	}
	if s.csrfMatches(csrf, token+"x") {
		t.Error("a CSRF token matches a different cookie")
	}
	if s.csrfMatches(csrf+"A", token) || s.csrfMatches("", token) {
		t.Error("a CSRF token of the wrong length matches")
	}
	if s.csrfMatches(strings.Repeat("A", maxCSRFHeader+1), token) {
		t.Error("an overlong CSRF token matches")
	}
	// The CSRF token is the HMAC of "csrf" and the cookie, not of the cookie alone.
	raw, err := sessionEncoding.DecodeString(csrf)
	if err != nil || len(raw) != sha256.Size {
		t.Fatalf("csrf token %q is not a base64url SHA-256", csrf)
	}
	if bytes.Equal(raw, s.tag([]byte(token))) {
		t.Error("CSRF token is the plain HMAC of the cookie, without the csrf label")
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	s := newTestService(t)
	set := s.sessionCookie("abc.def", 60)
	for _, want := range []string{"unio_session=abc.def", "Path=/unio/", "Max-Age=60", "HttpOnly", "SameSite=Strict"} {
		if !strings.Contains(set, want) {
			t.Errorf("Set-Cookie %q lacks %q", set, want)
		}
	}
	clear := s.sessionCookie("", -1)
	if !strings.Contains(clear, "Max-Age=0") {
		t.Errorf("clearing cookie %q does not expire it", clear)
	}
}

func TestSessionCookieValue(t *testing.T) {
	h := http.Header{}
	if got := sessionCookieValue(h); got != "" {
		t.Errorf("no cookie gave %q", got)
	}
	h.Set("Cookie", "a=1; unio_session=tok.en; b=2")
	if got := sessionCookieValue(h); got != "tok.en" {
		t.Errorf("cookie value = %q", got)
	}
}

// FuzzOpenSession feeds arbitrary cookie values to the session parser. It must
// never panic, and any token it accepts must be live and within its lifetime.
func FuzzOpenSession(f *testing.F) {
	s := newTestService(f)
	good, err := s.sealSession(sessionClaims{AccessKey: "ALICEKEY0000001", IssuedAt: testNow, ExpiresAt: testNow.Add(SessionTTL)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add("")
	f.Add(".")
	f.Add("a.b")
	f.Add(strings.Repeat("A", 200))
	f.Fuzz(func(t *testing.T, token string) {
		claims, err := s.openSession(token, testNow)
		if err != nil {
			return
		}
		if !testNow.Before(claims.ExpiresAt) {
			t.Fatalf("accepted a token that expired at %v", claims.ExpiresAt)
		}
		if claims.ExpiresAt.Sub(claims.IssuedAt) > SessionTTL {
			t.Fatalf("accepted a lifetime of %v", claims.ExpiresAt.Sub(claims.IssuedAt))
		}
	})
}

// newTestService returns a service with the test cluster secret, for unit tests
// of the session code. It does not need the rest of the fixture.
func newTestService(tb testing.TB) *Service {
	tb.Helper()
	key, err := deriveSessionKey([]byte(testSecret))
	if err != nil {
		tb.Fatal(err)
	}
	dummy, err := newDummySecret()
	if err != nil {
		tb.Fatal(err)
	}
	return &Service{key: key, dummy: dummy}
}
