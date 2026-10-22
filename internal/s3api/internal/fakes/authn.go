package fakes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// FakeAccessKey is the access key reported for every signed request.
const FakeAccessKey = "AKIAFAKE0000000000"

// FakeUser is the user reported for every signed request.
const FakeUser = "fake-user"

// Authenticator is a fake auth.Authenticator for tests.
//
// It does NOT verify signatures, presigned URLs or payload checksums. A
// request with an Authorization header is reported as the fake user and any
// other request as anonymous. Use it only in tests.
type Authenticator struct {
	// Err, when non-nil, is returned by Authenticate in place of a result.
	Err error
}

var _ auth.Authenticator = (*Authenticator)(nil)

// Authenticate implements auth.Authenticator without verifying the request.
// The payload is passed through unchanged, and the digest from the
// X-Amz-Content-Sha256 header is decoded when it is a 64-character hex value.
func (a *Authenticator) Authenticate(ctx context.Context, req *auth.Request) (*auth.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.Err != nil {
		return nil, a.Err
	}
	if req == nil {
		return nil, s3err.Errorf(s3err.InvalidRequest, "request is nil")
	}
	res := &auth.Result{
		Identity:      fakeIdentity(req.Header),
		PayloadSHA256: payloadDigest(req.Header),
		SignedTime:    time.Now().UTC().Truncate(time.Second),
	}
	if req.Body != nil {
		res.Body = req.Body
		res.DecodedLength = req.ContentLength
	}
	return res, nil
}

// fakeIdentity returns the identity of a request with the given headers.
func fakeIdentity(h http.Header) auth.Identity {
	if h.Get("Authorization") == "" {
		return auth.Identity{Anonymous: true}
	}
	return auth.Identity{AccessKey: FakeAccessKey, User: FakeUser}
}

// payloadDigest decodes the X-Amz-Content-Sha256 header when it holds a
// 64-character hex digest, and returns nil otherwise.
func payloadDigest(h http.Header) []byte {
	v := h.Get("X-Amz-Content-Sha256")
	if len(v) != sha256.Size*2 {
		return nil
	}
	d, err := hex.DecodeString(v)
	if err != nil {
		return nil
	}
	return d
}
