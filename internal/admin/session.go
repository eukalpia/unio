package admin

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Session constants. The cookie name and the HKDF info are part of the wire
// contract and must not change.
const (
	// SessionCookie is the name of the console session cookie.
	SessionCookie = "unio_session"
	// SessionTTL is the lifetime of a session, from its issue time.
	SessionTTL = 12 * time.Hour
	// CSRFHeader carries the CSRF token of a state-changing request.
	CSRFHeader = "X-Unio-CSRF"

	sessionInfo     = "unio/v1/console-session"
	sessionKeyLen   = 32
	minSecretLen    = 32
	cookiePath      = "/unio/"
	maxSessionToken = 1024
	maxCSRFHeader   = 256
	csrfLabel       = "csrf"
)

// sessionClaims is the payload of a session token. It names the access key
// the session belongs to, and the times it was issued and expires.
type sessionClaims struct {
	AccessKey string    `json:"accessKey"`
	IssuedAt  time.Time `json:"issuedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// deriveSessionKey derives the HMAC key of session tokens from the cluster
// secret with HKDF-SHA256, using the info "unio/v1/console-session".
func deriveSessionKey(secret []byte) ([]byte, error) {
	if len(secret) < minSecretLen {
		return nil, fmt.Errorf("cluster secret must be at least %d bytes", minSecretLen)
	}
	return hkdf.Key(sha256.New, secret, nil, sessionInfo, sessionKeyLen)
}

// newDummySecret returns a random value that a login with an unknown key is
// checked against, so that the check takes the same time for every key.
func newDummySecret() ([]byte, error) {
	b := make([]byte, sha256.Size)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("generate dummy secret: %w", err)
	}
	return b, nil
}

// tag returns HMAC-SHA256 of data under the session key.
func (s *Service) tag(data []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(data)
	return m.Sum(nil)
}

// sealSession returns the cookie value for claims: the base64url payload, a
// dot, and the base64url HMAC of the payload.
func (s *Service) sealSession(c sessionClaims) (string, error) {
	payload, err := json.Marshal(c) // #nosec G117 -- the access key ID is public; no secret is in the claims
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}
	return sessionEncoding.EncodeToString(payload) + "." + sessionEncoding.EncodeToString(s.tag(payload)), nil
}

// sessionEncoding is the strict unpadded base64url used by session tokens. A
// token has exactly one encoding, so a re-encoded token never verifies twice.
var sessionEncoding = base64.RawURLEncoding.Strict()

// errSessionInvalid is returned for every token that does not verify. The
// reason is never reported to the client.
var errSessionInvalid = errors.New("admin: session token does not verify")

// openSession verifies token and returns its claims. The HMAC is checked in
// constant time before the payload is parsed. The token must not have expired
// at now, and must have been issued for at most SessionTTL.
func (s *Service) openSession(token string, now time.Time) (sessionClaims, error) {
	if token == "" || len(token) > maxSessionToken {
		return sessionClaims{}, errSessionInvalid
	}
	dot := strings.IndexByte(token, '.')
	if dot < 0 || strings.IndexByte(token[dot+1:], '.') >= 0 {
		return sessionClaims{}, errSessionInvalid
	}
	payload, err := sessionEncoding.DecodeString(token[:dot])
	if err != nil {
		return sessionClaims{}, errSessionInvalid
	}
	sig, err := sessionEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return sessionClaims{}, errSessionInvalid
	}
	if !hmac.Equal(sig, s.tag(payload)) {
		return sessionClaims{}, errSessionInvalid
	}
	var c sessionClaims
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return sessionClaims{}, errSessionInvalid
	}
	if c.ExpiresAt.Sub(c.IssuedAt) > SessionTTL || !now.Before(c.ExpiresAt) {
		return sessionClaims{}, errSessionInvalid
	}
	return c, nil
}

// csrfToken returns the CSRF token of a session cookie value: the base64url
// HMAC of "csrf" followed by the cookie value. Only the holder of the cookie
// can compute it, and a page on another origin cannot read it.
func (s *Service) csrfToken(cookie string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(csrfLabel + cookie))
	return sessionEncoding.EncodeToString(m.Sum(nil))
}

// csrfMatches reports whether got is the CSRF token of cookie. The comparison
// is constant time, and an overlong header is refused before it runs.
func (s *Service) csrfMatches(got, cookie string) bool {
	if got == "" || len(got) > maxCSRFHeader {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.csrfToken(cookie))) == 1
}

// sessionCookie returns the Set-Cookie value for a session cookie. maxAge is in
// seconds; a negative value clears the cookie.
func (s *Service) sessionCookie(value string, maxAge int) string {
	c := &http.Cookie{ // #nosec G124 -- Secure follows the TLS configuration; a plain HTTP node cannot set it
		Name:     SessionCookie,
		Value:    value,
		Path:     cookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cfg.Secure,
		SameSite: http.SameSiteStrictMode,
	}
	return c.String()
}

// sessionCookieValue returns the value of the session cookie in h, or "" when
// the request carries none.
func sessionCookieValue(h http.Header) string {
	r := &http.Request{Header: h}
	c, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}
