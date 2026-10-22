package sigv4

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// CredentialStore resolves access keys to their secrets. Implementations
// return ErrUnknownAccessKey for keys they do not know or whose user is
// disabled. The secret is used only for key derivation and is never
// returned to clients.
type CredentialStore interface {
	Lookup(ctx context.Context, accessKey string) (secret string, identity auth.Identity, err error)
}

// Options configures an Authenticator. Zero values select the defaults.
type Options struct {
	// Region is the only region a credential scope may name. Default
	// us-east-1.
	Region string
	// Clock returns the current time. Default time.Now.
	Clock func() time.Time
	// MaxSkew is the largest allowed difference between the request time
	// and the server clock. Default 15 minutes.
	MaxSkew time.Duration
}

const (
	defaultRegion      = "us-east-1"
	defaultMaxSkew     = 15 * time.Minute
	signingKeyCacheLen = 256
)

// Authenticator verifies SigV4 signatures. It is safe for concurrent use.
type Authenticator struct {
	store   CredentialStore
	region  string
	clock   func() time.Time
	maxSkew time.Duration
	keys    *keyCache
}

var _ auth.Authenticator = (*Authenticator)(nil)

// New returns an Authenticator that resolves access keys through store.
func New(store CredentialStore, opts Options) *Authenticator {
	a := &Authenticator{
		store:   store,
		region:  opts.Region,
		clock:   opts.Clock,
		maxSkew: opts.MaxSkew,
		keys:    newKeyCache(signingKeyCacheLen),
	}
	if a.region == "" {
		a.region = defaultRegion
	}
	if a.clock == nil {
		a.clock = time.Now
	}
	if a.maxSkew <= 0 {
		a.maxSkew = defaultMaxSkew
	}
	return a
}

// Authenticate implements auth.Authenticator. A request with neither an
// Authorization header nor presigned parameters is anonymous.
func (a *Authenticator) Authenticate(ctx context.Context, req *auth.Request) (*auth.Result, error) {
	if req == nil {
		return nil, s3err.Errorf(s3err.InvalidRequest, "missing request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.store == nil {
		return nil, errors.New("sigv4: no credential store configured")
	}
	query, err := parseQuery(req.RawQuery)
	if err != nil {
		return nil, s3err.Wrap(s3err.InvalidRequest, err)
	}
	values := req.Header.Values("Authorization")
	presigned := hasParam(query, paramAlgorithm)
	switch {
	case len(values) > 1:
		return nil, malformed("multiple Authorization headers")
	case presigned && len(values) == 1:
		return nil, s3err.Errorf(s3err.AuthorizationQueryParametersError,
			"only one of the Authorization header and presigned parameters may be used")
	case presigned:
		return a.verifyPresigned(ctx, req, query)
	case len(values) == 1:
		return a.verifyHeader(ctx, req, query, values[0])
	}
	if hasAnyParam(query, presignParams...) {
		return nil, s3err.Errorf(s3err.AuthorizationQueryParametersError,
			"presigned parameters require X-Amz-Algorithm")
	}
	return &auth.Result{
		Identity:      auth.Identity{Anonymous: true},
		Body:          req.Body,
		DecodedLength: req.ContentLength,
	}, nil
}

// verifyHeader verifies a request signed in the Authorization header.
func (a *Authenticator) verifyHeader(ctx context.Context, req *auth.Request, query []queryParam, value string) (*auth.Result, error) {
	hdr, err := parseAuthorization(value)
	if err != nil {
		return nil, err
	}
	if err := a.checkScope(hdr.scope); err != nil {
		return nil, err
	}
	when, err := requestTime(req.Header)
	if err != nil {
		return nil, err
	}
	if err := checkScopeDate(hdr.scope, when); err != nil {
		return nil, err
	}
	if err := a.checkSkew(when.t); err != nil {
		return nil, err
	}
	required := []string{"host"}
	if when.header == "date" {
		// Without X-Amz-Date the Date header is the signing time, so it must
		// be signed.
		required = append(required, "date")
	}
	if err := requireSigned(hdr.signedHeaders, required...); err != nil {
		return nil, err
	}
	pv := req.Header.Values("X-Amz-Content-Sha256")
	if len(pv) == 0 {
		return nil, s3err.Errorf(s3err.InvalidRequest, "x-amz-content-sha256 is required")
	}
	if len(pv) > 1 {
		return nil, malformed("duplicate x-amz-content-sha256")
	}
	if err := requireHeadersSigned(req.Header, hdr.signedHeaders, false); err != nil {
		return nil, err
	}
	pl, err := parsePayloadHash(pv[0])
	if err != nil {
		return nil, err
	}
	id, key, err := a.resolve(ctx, hdr.scope)
	if err != nil {
		return nil, err
	}
	canon, err := canonicalRequest(canonicalSpec{
		Method:        req.Method,
		RawPath:       req.RawPath,
		Query:         query,
		SignedHeaders: hdr.signedHeaders,
		Header:        req.Header,
		Host:          req.Host,
		ContentLength: req.ContentLength,
		PayloadHash:   pv[0],
	})
	if err != nil {
		return nil, err
	}
	if err := verifySignature(key, when.amz, hdr, canon); err != nil {
		return nil, err
	}
	return buildResult(req, verified{id: id, pl: pl, t: when.t, amz: when.amz, scope: hdr.scope.String(), key: key, seed: hdr.signature})
}

// resolve looks up the access key and derives its signing key.
func (a *Authenticator) resolve(ctx context.Context, scope credentialScope) (auth.Identity, [32]byte, error) {
	secret, id, err := a.store.Lookup(ctx, scope.accessKey)
	if err != nil {
		if errors.Is(err, ErrUnknownAccessKey) {
			return auth.Identity{}, [32]byte{}, ErrUnknownAccessKey
		}
		return auth.Identity{}, [32]byte{}, fmt.Errorf("sigv4: credential lookup: %w", err)
	}
	id.AccessKey = scope.accessKey
	return id, a.signingKey(secret, scope.date), nil
}

// verifySignature recomputes the signature over the canonical request and
// compares it with the one from the request in constant time.
func verifySignature(key [32]byte, amzDate string, hdr authorization, canon string) error {
	sts := stringToSign(amzDate, hdr.scope.String(), sha256Hex(canon))
	sig := hmacSHA256(key[:], []byte(sts))
	if !hmac.Equal(sig[:], hdr.signature[:]) {
		return s3err.Errorf(s3err.SignatureDoesNotMatch, "the request signature does not match")
	}
	return nil
}

// checkScope rejects credential scopes for another region. The service is
// checked by parseCredential.
func (a *Authenticator) checkScope(s credentialScope) error {
	if s.region != a.region {
		return malformed("the credential scope names region %q, expected %q", s.region, a.region)
	}
	return nil
}

// checkScopeDate requires the credential date to match the request time.
func checkScopeDate(s credentialScope, when signingTime) error {
	if s.date != when.t.Format(dateLayout) {
		return malformed("the credential date does not match the request time")
	}
	return nil
}

// checkSkew rejects request times further than maxSkew from the server clock
// in either direction. Used for header-signed requests, whose signature is
// only meaningful for a short window.
func (a *Authenticator) checkSkew(t time.Time) error {
	d := a.clock().Sub(t)
	if d < 0 {
		d = -d
	}
	if d > a.maxSkew {
		return errTimeSkew()
	}
	return nil
}

// checkNotFuture rejects a presigned request time more than maxSkew ahead of
// the server clock. A presigned URL may legitimately be older than maxSkew:
// its age is bounded by X-Amz-Expires instead.
func (a *Authenticator) checkNotFuture(t time.Time) error {
	if t.Sub(a.clock()) > a.maxSkew {
		return errTimeSkew()
	}
	return nil
}

// errTimeSkew is the error for a request time outside the allowed window.
func errTimeSkew() error {
	return s3err.Errorf(s3err.RequestTimeTooSkewed, "the difference between the request time and the server's time is too large")
}

// requireSigned checks that each named header is in the signed list. The
// host must always be signed, or a client could change it in transit without
// invalidating the signature. Headers that are present in the request are
// checked against the signed list by requireHeadersSigned.
func requireSigned(signed []string, required ...string) error {
	for _, n := range required {
		if !slices.Contains(signed, n) {
			return malformed("%s must be a signed header", n)
		}
	}
	return nil
}
