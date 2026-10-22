package obs

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler serves the registry of m in the Prometheus text format. The
// handler is wrapped by BearerAuth, so it requires token.
func MetricsHandler(m *Metrics, token []byte) (http.Handler, error) {
	if m == nil {
		return nil, errors.New("obs: metrics handler requires metrics")
	}
	return BearerAuth(token, promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
}

// BearerAuth requires "Authorization: Bearer <token>" before it calls next.
//
// The presented token and the configured token are each hashed with SHA-256,
// and the digests are compared with crypto/subtle. The comparison therefore
// takes the same time for any token length and does not leak the length of
// the secret. The configured token is not retained after construction.
//
// A missing or wrong token gets 401 with a WWW-Authenticate: Bearer header and
// an empty body. An empty token is a constructor error, so an endpoint can
// never be left open by accident.
func BearerAuth(token []byte, next http.Handler) (http.Handler, error) {
	if len(token) == 0 {
		return nil, errors.New("obs: bearer token must not be empty")
	}
	if next == nil {
		return nil, errors.New("obs: bearer auth requires a handler")
	}
	want := sha256.Sum256(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented, ok := bearerToken(r.Header.Get("Authorization"))
		if !ok || !digestMatches(presented, want) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

// bearerToken extracts the token from an Authorization header value. The
// scheme is matched case-insensitively, as RFC 6750 allows.
func bearerToken(header string) (string, bool) {
	scheme, tok, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || tok == "" {
		return "", false
	}
	return tok, true
}

// digestMatches reports, in constant time, whether presented hashes to want.
func digestMatches(presented string, want [sha256.Size]byte) bool {
	got := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}
