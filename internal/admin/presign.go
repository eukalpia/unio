package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/sigv4"
	"github.com/eukalpia/unio/internal/object"
)

// Limits of a presigned URL, in seconds. A presign request outside them is
// refused.
const (
	presignMinSeconds = 1
	presignMaxSeconds = 3600
)

// presignService is the signing service name of S3 URLs.
const presignService = "s3"

// presignBody is the body of POST buckets/{name}/presign.
type presignBody struct {
	Method         string `json:"method"`
	Key            string `json:"key"`
	ExpiresSeconds int64  `json:"expiresSeconds"`
}

// presignReply is the body of a presign response.
type presignReply struct {
	URL string `json:"url"`
}

// presign answers POST buckets/{name}/presign. The URL is signed with the
// access key of the session, so it carries that key's rights: IAM is checked
// again when the URL is used, and this call also checks the right to the one
// action before anything is signed. A URL for the root account is signed with
// the root secret only when the root account is the session.
func (s *Service) presign(c *call) (Response, error) {
	bucket := c.params[0]
	if err := object.ValidateBucketName(bucket); err != nil {
		return Response{}, err
	}
	var in presignBody
	if err := decodeBody(c, &in); err != nil {
		return Response{}, err
	}
	action, method, err := presignAction(in.Method)
	if err != nil {
		return Response{}, err
	}
	if err := object.ValidateKey(in.Key); err != nil {
		return Response{}, err
	}
	if in.ExpiresSeconds < presignMinSeconds || in.ExpiresSeconds > presignMaxSeconds {
		return Response{}, errInvalid("expiresSeconds must be between 1 and 3600.")
	}
	if !validHost(c.req.Host) {
		return Response{}, errBadRequest("The Host header is not valid.")
	}
	if err := s.cfg.IAM.Authorize(c.ctx, c.p.identity, action, bucket, in.Key); err != nil {
		return Response{}, authzError(err)
	}
	secret, _, err := s.cfg.Credentials.Lookup(c.ctx, c.p.identity.AccessKey)
	if err != nil {
		if errors.Is(err, sigv4.ErrUnknownAccessKey) {
			return Response{}, errUnauthenticated()
		}
		return Response{}, err
	}
	creds := aws.Credentials{AccessKeyID: c.p.identity.AccessKey, SecretAccessKey: secret}
	signed, err := s.signURL(c.ctx, creds, method, c.req.Host, bucket, in.Key, in.ExpiresSeconds)
	if err != nil {
		return Response{}, err
	}
	return jsonResponse(http.StatusOK, presignReply{URL: signed})
}

// presignAction maps a presign method to the IAM action it grants, and to the
// HTTP method of the URL.
func presignAction(method string) (auth.Action, string, error) {
	switch method {
	case http.MethodGet:
		return auth.ActionGetObject, http.MethodGet, nil
	case http.MethodPut:
		return auth.ActionPutObject, http.MethodPut, nil
	}
	return "", "", errInvalid("method must be GET or PUT.")
}

// signURL returns a SigV4 presigned URL for method on bucket/key, valid for
// expires seconds from now. The path is escaped once, by the URL, and the signer
// uses that escaping as is, so the canonical request matches the URL a client
// sends.
func (s *Service) signURL(ctx context.Context, creds aws.Credentials, method, host, bucket, key string, expires int64) (string, error) {
	scheme := "http"
	if s.cfg.Secure {
		scheme = "https"
	}
	u := &url.URL{Scheme: scheme, Host: host, Path: "/" + bucket + "/" + key}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("admin: presign request: %w", err)
	}
	q := req.URL.Query()
	q.Set("X-Amz-Expires", strconv.FormatInt(expires, 10))
	req.URL.RawQuery = q.Encode()
	signed, _, err := v4.NewSigner().PresignHTTP(ctx, creds, req, unsignedPayload, presignService, s.cfg.Region, s.cfg.Clock(),
		func(o *v4.SignerOptions) { o.DisableURIPathEscaping = true })
	if err != nil {
		return "", fmt.Errorf("admin: presign: %w", err)
	}
	return signed, nil
}
