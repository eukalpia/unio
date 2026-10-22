package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/sigv4"
)

// unsignedPayload is the SigV4 payload hash that leaves the body unsigned.
const unsignedPayload = "UNSIGNED-PAYLOAD"

// principal is the caller of one request, once it has been authenticated and
// shown to hold unio:Admin.
type principal struct {
	identity auth.Identity
	// viaCookie is true when the session cookie authenticated the request. Only
	// such a request must carry the CSRF token.
	viaCookie bool
	// cookie is the session cookie value that the CSRF token is derived from.
	cookie string
	// expiresAt is the session expiry. It is zero for a SigV4 caller.
	expiresAt time.Time
	// body is the reader that the request body is read from: the verified
	// payload for SigV4, and the raw body for a session cookie.
	body io.Reader
	// unsigned is true when the SigV4 payload hash is UNSIGNED-PAYLOAD, so the
	// body is not covered by the signature.
	unsigned bool
}

// hasSigV4 reports whether the request is signed: by an Authorization header
// or by presigned query parameters. Such a request is never judged by its
// cookie, even when a cookie is also present.
func hasSigV4(h http.Header, query url.Values) bool {
	if len(h.Values("Authorization")) > 0 {
		return true
	}
	_, presigned := query["X-Amz-Algorithm"]
	return presigned
}

// authenticate identifies the caller of req, and checks that it holds
// unio:Admin. A signed request is verified by the IAM store. Otherwise the
// session cookie must verify and its access key must still be enabled and
// still hold unio:Admin, so that a revocation takes effect at the IAM cache
// time.
func (s *Service) authenticate(ctx context.Context, req Request, query url.Values) (principal, error) {
	if hasSigV4(req.Header, query) {
		return s.authenticateSigV4(ctx, req, query)
	}
	return s.authenticateSession(ctx, req)
}

// authenticateSigV4 verifies a signed request and checks its right. A presigned
// request, and one that declares UNSIGNED-PAYLOAD, does not cover its body, so
// its body must be empty.
func (s *Service) authenticateSigV4(ctx context.Context, req Request, query url.Values) (principal, error) {
	_, presigned := query["X-Amz-Algorithm"]
	res, err := s.cfg.IAM.Authenticate(ctx, &auth.Request{
		Method:        req.Method,
		Host:          req.Host,
		RawPath:       req.Path,
		RawQuery:      req.Query,
		Header:        req.Header,
		Body:          req.Body,
		ContentLength: req.ContentLength,
	})
	if err != nil {
		return principal{}, authzError(err)
	}
	if res.Identity.Anonymous || res.Identity.AccessKey == "" {
		return principal{}, errDenied()
	}
	if err := s.cfg.IAM.Authorize(ctx, res.Identity, auth.ActionAdmin, "", ""); err != nil {
		return principal{}, authzError(err)
	}
	return principal{
		identity: res.Identity,
		body:     res.Body,
		unsigned: presigned || req.Header.Get("X-Amz-Content-Sha256") == unsignedPayload,
	}, nil
}

// authenticateSession verifies the session cookie, then checks the access key
// against IAM. Every failure is 401, so a client can tell that it must log in
// again.
func (s *Service) authenticateSession(ctx context.Context, req Request) (principal, error) {
	cookie := sessionCookieValue(req.Header)
	if cookie == "" {
		return principal{}, errUnauthenticated()
	}
	claims, err := s.openSession(cookie, s.cfg.Clock())
	if err != nil {
		return principal{}, errUnauthenticated()
	}
	_, id, err := s.cfg.Credentials.Lookup(ctx, claims.AccessKey)
	if err != nil {
		if errors.Is(err, sigv4.ErrUnknownAccessKey) {
			return principal{}, errUnauthenticated()
		}
		return principal{}, sessionError(err)
	}
	if err := s.cfg.IAM.Authorize(ctx, id, auth.ActionAdmin, "", ""); err != nil {
		return principal{}, sessionError(err)
	}
	id.AccessKey = claims.AccessKey
	return principal{
		identity:  id,
		viaCookie: true,
		cookie:    cookie,
		expiresAt: claims.ExpiresAt,
		body:      req.Body,
	}, nil
}

// sessionError maps a failure of a session check. A store failure keeps its
// status; any refusal is 401.
func sessionError(err error) error {
	if storeFailure(err) {
		return err
	}
	return errUnauthenticated()
}

// checkCSRF requires the CSRF token on a request that changes state and that
// the session cookie authenticated. The token must be given exactly once.
// Requests signed with SigV4 and reads are not checked.
func (s *Service) checkCSRF(req Request, p principal) error {
	if !p.viaCookie || req.Method == http.MethodGet || req.Method == http.MethodHead {
		return nil
	}
	vals := req.Header.Values(CSRFHeader)
	if len(vals) != 1 || !s.csrfMatches(vals[0], p.cookie) {
		return errDenied()
	}
	return nil
}

// requestBody reads the body of an authenticated request, bounded. A body that
// a SigV4 signature did not cover is refused.
func (s *Service) requestBody(p principal) ([]byte, error) {
	body, err := readBody(p.body)
	if err != nil {
		return nil, err
	}
	if p.unsigned && len(body) > 0 {
		return nil, errDenied()
	}
	return body, nil
}
