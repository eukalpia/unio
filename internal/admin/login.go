package admin

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/sigv4"
)

// maxAccessKeyLen and maxSecretLen bound the credentials of a login. A longer
// value cannot be a credential, so it is refused as a wrong one.
const (
	maxAccessKeyLen = 128
	maxSecretLen    = 256
)

// loginRequest is the body of POST session.
type loginRequest struct {
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// sessionReply is the body of a successful POST session.
type sessionReply struct {
	AccessKey string `json:"accessKey"`
	ExpiresAt string `json:"expiresAt"`
	CSRF      string `json:"csrf"`
}

// sessionView is the body of GET session. ExpiresAt is null for a caller that
// signed each request and has no session.
type sessionView struct {
	AccessKey string  `json:"accessKey"`
	ExpiresAt *string `json:"expiresAt"`
}

// login checks an access key and secret, and on success issues a session
// cookie and the CSRF token. Each client is limited to a few attempts per
// window, and the limit is checked before the body is read. A wrong key, a
// wrong secret, a disabled user and a missing right all give the same
// AccessDenied, so the response reveals nothing about which one it was.
func (s *Service) login(c *call) (Response, error) {
	if ok, wait := s.limiter.allow(clientKey(c.req.RemoteAddr)); !ok {
		resp := errorResponse(c.req.RequestID, errLoginLimited())
		resp.Header.Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
		return resp, nil
	}
	body, err := readBody(c.req.Body)
	if err != nil {
		return Response{}, err
	}
	c.body = body
	var in loginRequest
	if err := decodeBody(c, &in); err != nil {
		return Response{}, err
	}
	if in.AccessKey == "" || len(in.AccessKey) > maxAccessKeyLen || len(in.SecretKey) > maxSecretLen {
		return Response{}, errDenied()
	}

	secret, id, err := s.cfg.Credentials.Lookup(c.ctx, in.AccessKey)
	found := err == nil
	if err != nil {
		if !errors.Is(err, sigv4.ErrUnknownAccessKey) {
			return Response{}, err
		}
		// Compare with a dummy, so an unknown key takes the same time as a
		// wrong secret.
		secret = string(s.dummy)
	}
	want := sha256.Sum256([]byte(secret))
	got := sha256.Sum256([]byte(in.SecretKey))
	if !found || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		return Response{}, errDenied()
	}
	if err := s.cfg.IAM.Authorize(c.ctx, id, auth.ActionAdmin, "", ""); err != nil {
		return Response{}, authzError(err)
	}

	now := s.cfg.Clock().UTC().Truncate(time.Second)
	claims := sessionClaims{AccessKey: in.AccessKey, IssuedAt: now, ExpiresAt: now.Add(SessionTTL)}
	token, err := s.sealSession(claims)
	if err != nil {
		return Response{}, err
	}
	resp, err := jsonResponse(http.StatusOK, sessionReply{
		AccessKey: in.AccessKey,
		ExpiresAt: claims.ExpiresAt.Format(time.RFC3339),
		CSRF:      s.csrfToken(token),
	})
	if err != nil {
		return Response{}, err
	}
	resp.Header.Add("Set-Cookie", s.sessionCookie(token, int(SessionTTL/time.Second)))
	s.cfg.Logger.Info("admin session opened", "request_id", c.req.RequestID, "remote", c.req.RemoteAddr)
	return resp, nil
}

// sessionGet describes the session of an authenticated caller.
func (s *Service) sessionGet(c *call) (Response, error) {
	view := sessionView{AccessKey: c.p.identity.AccessKey}
	if !c.p.expiresAt.IsZero() {
		v := c.p.expiresAt.UTC().Format(time.RFC3339)
		view.ExpiresAt = &v
	}
	return jsonResponse(http.StatusOK, view)
}

// logout clears the session cookie. A logout needs no session: it changes only
// the cookie of the client that sends it, and the session token expires on its
// own within SessionTTL.
func (s *Service) logout(c *call) (Response, error) {
	resp := emptyResponse(http.StatusNoContent)
	resp.Header.Add("Set-Cookie", s.sessionCookie("", -1))
	return resp, nil
}

// retrySeconds returns the wait in whole seconds, rounded up, and at least 1.
func retrySeconds(wait time.Duration) int {
	secs := int(math.Ceil(wait.Seconds()))
	if secs < 1 {
		return 1
	}
	return secs
}
