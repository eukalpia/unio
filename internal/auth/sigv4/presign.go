package sigv4

import (
	"context"
	"errors"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// Query parameters of a presigned request.
const (
	paramAlgorithm     = "X-Amz-Algorithm"
	paramCredential    = "X-Amz-Credential" // #nosec G101 -- public query parameter name, not a secret
	paramDate          = "X-Amz-Date"
	paramExpires       = "X-Amz-Expires"
	paramSignedHeaders = "X-Amz-SignedHeaders"
	paramSignature     = "X-Amz-Signature"
)

// maxPresignExpires is the longest lifetime a presigned URL may declare:
// seven days.
const maxPresignExpires = 604800

// presignParams lists every parameter that belongs to presigned auth.
var presignParams = []string{
	paramAlgorithm, paramCredential, paramDate, paramExpires, paramSignedHeaders, paramSignature,
}

// hasParam reports whether the decoded query contains name.
func hasParam(query []queryParam, name string) bool {
	for _, p := range query {
		if p.key == name {
			return true
		}
	}
	return false
}

// hasAnyParam reports whether the decoded query contains any of names.
func hasAnyParam(query []queryParam, names ...string) bool {
	for _, n := range names {
		if hasParam(query, n) {
			return true
		}
	}
	return false
}

// singleParam returns the value of a parameter that must appear exactly
// once. Repeated or missing parameters are malformed.
func singleParam(query []queryParam, name string) (string, error) {
	var val string
	count := 0
	for _, p := range query {
		if p.key == name {
			val = p.value
			count++
		}
	}
	switch count {
	case 1:
		return val, nil
	case 0:
		return "", malformed("missing %s", name)
	default:
		return "", malformed("duplicate %s", name)
	}
}

// parseExpires parses X-Amz-Expires: a decimal number of seconds between 1
// and maxPresignExpires.
func parseExpires(v string) (time.Duration, error) {
	if len(v) == 0 || len(v) > 6 || !allChars(v, isDigit) {
		return 0, malformed("malformed X-Amz-Expires")
	}
	n := 0
	for i := 0; i < len(v); i++ {
		n = n*10 + int(v[i]-'0')
	}
	if n < 1 || n > maxPresignExpires {
		return 0, malformed("X-Amz-Expires must be between 1 and %d", maxPresignExpires)
	}
	return time.Duration(n) * time.Second, nil
}

// isDigit reports whether c is an ASCII decimal digit.
func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

// verifyPresigned verifies a request authenticated by query parameters. A
// malformed parameter is reported with AuthorizationQueryParametersError,
// the code S3 uses for query authentication, rather than the header code.
func (a *Authenticator) verifyPresigned(ctx context.Context, req *auth.Request, query []queryParam) (*auth.Result, error) {
	res, err := a.verifyPresignedParams(ctx, req, query)
	return res, queryError(err)
}

// queryError maps a malformed-parameter error to the query code. The shared
// parsers report AuthorizationHeaderMalformed; on the presigned path every
// such error is a query parameter error. Other codes, such as AccessDenied
// for an expired URL, pass through unchanged.
func queryError(err error) error {
	var e *s3err.Error
	if errors.As(err, &e) && e.Code == s3err.AuthorizationHeaderMalformed {
		return &s3err.Error{Code: s3err.AuthorizationQueryParametersError, Message: e.Message, Err: e.Err}
	}
	return err
}

// verifyPresignedParams checks the presigned parameters and the signature. The
// payload of a presigned request is always UNSIGNED-PAYLOAD and the
// signature itself is left out of the canonical query.
func (a *Authenticator) verifyPresignedParams(ctx context.Context, req *auth.Request, query []queryParam) (*auth.Result, error) {
	alg, err := singleParam(query, paramAlgorithm)
	if err != nil {
		return nil, err
	}
	if alg != algorithm {
		return nil, malformed("unsupported X-Amz-Algorithm")
	}
	cred, err := singleParam(query, paramCredential)
	if err != nil {
		return nil, err
	}
	dateV, err := singleParam(query, paramDate)
	if err != nil {
		return nil, err
	}
	expV, err := singleParam(query, paramExpires)
	if err != nil {
		return nil, err
	}
	signedV, err := singleParam(query, paramSignedHeaders)
	if err != nil {
		return nil, err
	}
	sigV, err := singleParam(query, paramSignature)
	if err != nil {
		return nil, err
	}
	hdr, err := buildAuthorization(cred, signedV, sigV)
	if err != nil {
		return nil, err
	}
	if err := a.checkScope(hdr.scope); err != nil {
		return nil, err
	}
	t, err := parseAmzDate(dateV)
	if err != nil {
		return nil, err
	}
	when := signingTime{t: t, amz: dateV, header: paramDate}
	if err := checkScopeDate(hdr.scope, when); err != nil {
		return nil, err
	}
	expires, err := parseExpires(expV)
	if err != nil {
		return nil, err
	}
	if !a.clock().Before(t.Add(expires)) {
		return nil, s3err.Errorf(s3err.AccessDenied, "Request has expired")
	}
	if err := a.checkNotFuture(t); err != nil {
		return nil, err
	}
	if err := requireSigned(hdr.signedHeaders, "host"); err != nil {
		return nil, err
	}
	if v := req.Header.Values("X-Amz-Content-Sha256"); len(v) > 0 {
		// A streaming marker on a presigned request would leave aws-chunked
		// framing in the body, so it is refused before the generic check.
		if pl, err := parsePayloadHash(v[0]); err == nil && pl.kind != payloadUnsigned && pl.kind != payloadDigest {
			return nil, s3err.Errorf(s3err.InvalidRequest, "streaming payloads cannot be presigned")
		}
	}
	if err := requireHeadersSigned(req.Header, hdr.signedHeaders); err != nil {
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
		ExcludeQuery:  paramSignature,
		SignedHeaders: hdr.signedHeaders,
		Header:        req.Header,
		Host:          req.Host,
		ContentLength: req.ContentLength,
		PayloadHash:   unsignedPayload,
	})
	if err != nil {
		return nil, err
	}
	if err := verifySignature(key, dateV, hdr, canon); err != nil {
		return nil, err
	}
	return buildResult(req, verified{id: id, pl: payload{kind: payloadUnsigned}, t: t, amz: dateV, scope: hdr.scope.String(), key: key, seed: hdr.signature})
}
