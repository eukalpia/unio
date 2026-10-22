package core

import (
	"context"
	"io"
	"net/http"
	"time"
)

// Identity is an authenticated principal.
type Identity struct {
	AccessKey string
	User      string
	// Root is true for the root credentials, which bypass policies.
	Root bool
	// Anonymous is true for unsigned requests.
	Anonymous bool
}

// AuthRequest is the transport-independent view of an S3 request that
// authentication needs. It is built by the S3 front end.
type AuthRequest struct {
	Method string
	Host   string
	// RawPath is the request path exactly as sent (still percent-encoded).
	RawPath  string
	RawQuery string
	// Header uses canonical MIME header keys.
	Header http.Header
	// Body is the raw request body; nil when there is none.
	Body io.Reader
	// ContentLength is the raw body length, or -1 if unknown.
	ContentLength int64
}

// AuthResult is the outcome of authenticating a request.
type AuthResult struct {
	Identity Identity
	// Body is the decoded payload: aws-chunked framing removed and every
	// chunk signature verified as it is read. It returns an error (never
	// io.EOF) if verification fails. nil when the request has no body.
	Body io.Reader
	// DecodedLength is the exact payload length (x-amz-decoded-content-length
	// for aws-chunked, else Content-Length), or -1 if unknown.
	DecodedLength int64
	// PayloadSHA256 is the expected SHA-256 of the payload when the client
	// signed it (x-amz-content-sha256 holds a digest), else nil.
	PayloadSHA256 []byte
	// Trailer, when non-nil, returns the trailing checksum sent after an
	// aws-chunked body. It is valid only after Body returned io.EOF.
	Trailer func() (Checksum, bool)
	// SignedTime is the request time from X-Amz-Date or the presigned URL.
	SignedTime time.Time
}

// Authenticator verifies request signatures. Implemented by internal/auth.
type Authenticator interface {
	// Authenticate verifies SigV4 (header, presigned, streaming variants).
	// Unsigned requests yield an Anonymous identity. Failures are *Error
	// values with ErrSignatureDoesNotMatch, ErrInvalidAccessKeyID,
	// ErrRequestTimeTooSkewed, ErrAuthorizationHeaderMalformed, ...
	Authenticate(ctx context.Context, req *AuthRequest) (*AuthResult, error)
}

// Action is an S3 IAM action name such as "s3:GetObject".
type Action string

// IAM actions used by the v0.1 API.
const (
	ActionCreateBucket               Action = "s3:CreateBucket"
	ActionDeleteBucket               Action = "s3:DeleteBucket"
	ActionListAllMyBuckets           Action = "s3:ListAllMyBuckets"
	ActionListBucket                 Action = "s3:ListBucket"
	ActionListBucketMultipartUploads Action = "s3:ListBucketMultipartUploads"
	ActionGetBucketLocation          Action = "s3:GetBucketLocation"
	ActionGetObject                  Action = "s3:GetObject"
	ActionPutObject                  Action = "s3:PutObject"
	ActionDeleteObject               Action = "s3:DeleteObject"
	ActionAbortMultipartUpload       Action = "s3:AbortMultipartUpload"
	ActionListMultipartUploadParts   Action = "s3:ListMultipartUploadParts"
	ActionGetObjectTagging           Action = "s3:GetObjectTagging"
	ActionPutObjectTagging           Action = "s3:PutObjectTagging"
	ActionAdmin                      Action = "unio:Admin"
)

// Authorizer decides whether an identity may perform an action.
type Authorizer interface {
	// Authorize returns nil or an *Error with ErrAccessDenied. bucket and
	// key may be empty for service-level actions.
	Authorize(ctx context.Context, id Identity, action Action, bucket, key string) error
}

// UserInfo describes an IAM user.
type UserInfo struct {
	Name    string
	Created time.Time
	Enabled bool
}

// AccessKey is a credential pair. Secret is only populated when the key is
// created and is never retrievable afterwards.
type AccessKey struct {
	ID      string
	Secret  string
	User    string
	Created time.Time
}

// IAM manages users, keys and policies. Implemented by internal/auth on top
// of SystemKV. Secrets are stored encrypted.
type IAM interface {
	Authenticator
	Authorizer
	CreateUser(ctx context.Context, name string) (UserInfo, error)
	DeleteUser(ctx context.Context, name string) error
	ListUsers(ctx context.Context) ([]UserInfo, error)
	CreateAccessKey(ctx context.Context, user string) (AccessKey, error)
	DeleteAccessKey(ctx context.Context, accessKeyID string) error
	ListAccessKeys(ctx context.Context, user string) ([]AccessKey, error)
	// SetUserPolicy attaches an IAM policy document (JSON) to a user,
	// replacing any previous one. An empty policy detaches it.
	SetUserPolicy(ctx context.Context, user string, policy []byte) error
}
