// Package handlers implements the S3 operations of the front end. Each
// handler reads its request through the parsers of package headers and
// xmldec, calls the object layer, and writes the S3 response. Handlers return
// *s3err.Error values for client errors; the server renders them.
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/reqstat"
	"github.com/eukalpia/unio/internal/s3api/router"
	"github.com/eukalpia/unio/internal/s3err"
)

// Env holds what every operation needs. It is immutable after construction
// and safe for concurrent use.
type Env struct {
	// Layer is the object storage API.
	Layer object.Layer
	// Region is the bucket location of this deployment.
	Region string
	// Logger receives diagnostics from handlers.
	Logger *slog.Logger
	// CopyPool supplies the copy buffers of streamed GET responses.
	CopyPool *bufpool.Pool
	// WriteIdle bounds the wait for each write of a streamed body.
	WriteIdle time.Duration
	// WriteCap caps the total time of one streamed response. Zero means none.
	WriteCap time.Duration
}

// Call is one authenticated request ready for its operation.
type Call struct {
	// Ctx is the request context; it is cancelled when the client goes away.
	Ctx context.Context
	// RC is the Hertz request context, used to read the request and write the
	// response.
	RC *app.RequestContext
	// Route is the operation and its bucket, key and query.
	Route router.Route
	// Header holds the request headers with canonical keys.
	Header http.Header
	// Auth is the authentication result. Its Body is the raw, not yet verified,
	// request payload; handlers that read it wrap it with payload.New.
	Auth *auth.Result
	// Authorize checks one more action, for operations that touch a second
	// object (CopyObject's source, each key of DeleteObjects).
	Authorize func(action auth.Action, bucket, key string) error
	// Resource names the bucket or object for error responses.
	Resource string
	// Stats records the streamed bytes of the response for the Observer. It
	// may be nil.
	Stats *reqstat.Stats
}

// Handle runs the handler for op.
func (e *Env) Handle(op router.Op, c *Call) error {
	if encryptionRefused(op) {
		if err := headers.RejectEncryption(c.Header); err != nil {
			return err
		}
	}
	switch op {
	case router.OpListBuckets:
		return e.ListBuckets(c)
	case router.OpCreateBucket:
		return e.CreateBucket(c)
	case router.OpHeadBucket:
		return e.HeadBucket(c)
	case router.OpDeleteBucket:
		return e.DeleteBucket(c)
	case router.OpGetBucketLocation:
		return e.GetBucketLocation(c)
	case router.OpListObjectsV1:
		return e.ListObjectsV1(c)
	case router.OpListObjectsV2:
		return e.ListObjectsV2(c)
	case router.OpListMultipartUploads:
		return e.ListMultipartUploads(c)
	case router.OpDeleteObjects:
		return e.DeleteObjects(c)
	case router.OpPutObject:
		return e.PutObject(c)
	case router.OpCopyObject:
		return e.CopyObject(c)
	case router.OpGetObject:
		return e.GetObject(c, false)
	case router.OpHeadObject:
		return e.GetObject(c, true)
	case router.OpDeleteObject:
		return e.DeleteObject(c)
	case router.OpCreateMultipartUpload:
		return e.CreateMultipartUpload(c)
	case router.OpUploadPart:
		return e.UploadPart(c)
	case router.OpUploadPartCopy:
		return e.UploadPartCopy(c)
	case router.OpCompleteMultipartUpload:
		return e.CompleteMultipartUpload(c)
	case router.OpAbortMultipartUpload:
		return e.AbortMultipartUpload(c)
	case router.OpListParts:
		return e.ListParts(c)
	case router.OpPutObjectTagging:
		return e.PutObjectTagging(c)
	case router.OpGetObjectTagging:
		return e.GetObjectTagging(c)
	case router.OpDeleteObjectTagging:
		return e.DeleteObjectTagging(c)
	}
	return s3err.Errorf(s3err.NotImplemented, "this operation is not supported")
}

// ownerName is the owner reported for every bucket and object. Multi-tenant
// ownership is outside the v0.1 scope, so the caller's user name stands in.
func (c *Call) ownerName() string {
	if c.Auth != nil && c.Auth.Identity.User != "" {
		return c.Auth.Identity.User
	}
	return "unio"
}

// writeXML sends an XML response with the given status.
func writeXML(c *Call, status int, body []byte) {
	c.RC.Data(status, "application/xml", body)
}

// writeStatus sends a response with no body.
func writeStatus(c *Call, status int) {
	c.RC.SetStatusCode(status)
	c.RC.Response.Header.SetContentLength(0)
}

// queryInt parses an optional integer query parameter in [lo, hi]. A missing
// parameter yields def. The value must be plain decimal digits; see boundedInt.
func queryInt(q url.Values, name string, def, lo, hi int) (int, error) {
	raw, ok := q[name]
	if !ok || len(raw) == 0 || raw[0] == "" {
		return def, nil
	}
	n, err := boundedInt(raw[0], lo, hi)
	if err != nil {
		return 0, s3err.Errorf(s3err.InvalidArgument, "%s must be an integer between %d and %d", name, lo, hi)
	}
	return n, nil
}

// httpTime formats t as an RFC 9110 HTTP date.
func httpTime(t time.Time) string {
	return t.UTC().Format(http.TimeFormat)
}

// encryptionRefused reports whether requests of op are refused when they carry
// a server-side encryption header. These are the operations that store or
// return object data, so an encryption request would be silently ignored.
func encryptionRefused(op router.Op) bool {
	switch op {
	case router.OpPutObject, router.OpCopyObject, router.OpCreateMultipartUpload,
		router.OpUploadPart, router.OpUploadPartCopy, router.OpGetObject, router.OpHeadObject:
		return true
	}
	return false
}
