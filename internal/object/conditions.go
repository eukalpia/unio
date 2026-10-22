package object

import (
	"time"

	"github.com/eukalpia/unio/internal/s3err"
)

// Conditions are the HTTP preconditions of a read or write.
type Conditions struct {
	// IfMatch is an ETag (unquoted) or "*".
	IfMatch string
	// IfNoneMatch is an ETag (unquoted) or "*".
	IfNoneMatch       string
	IfModifiedSince   time.Time
	IfUnmodifiedSince time.Time
}

// IsZero reports whether no condition is set.
func (c Conditions) IsZero() bool {
	return c.IfMatch == "" && c.IfNoneMatch == "" && c.IfModifiedSince.IsZero() && c.IfUnmodifiedSince.IsZero()
}

// EvaluateRead applies S3 read precondition semantics (RFC 9110 order) to
// an existing object. It returns nil, or an error with PreconditionFailed
// or NotModified.
func (c Conditions) EvaluateRead(info Info) error {
	mod := info.ModTime.Truncate(time.Second)
	if c.IfMatch != "" && c.IfMatch != "*" && c.IfMatch != info.ETag {
		return s3err.Errorf(s3err.PreconditionFailed, "If-Match failed")
	}
	if c.IfMatch == "" && !c.IfUnmodifiedSince.IsZero() && mod.After(c.IfUnmodifiedSince) {
		return s3err.Errorf(s3err.PreconditionFailed, "If-Unmodified-Since failed")
	}
	if c.IfNoneMatch != "" && (c.IfNoneMatch == "*" || c.IfNoneMatch == info.ETag) {
		return s3err.Errorf(s3err.NotModified, "If-None-Match matched")
	}
	if c.IfNoneMatch == "" && !c.IfModifiedSince.IsZero() && !mod.After(c.IfModifiedSince) {
		return s3err.Errorf(s3err.NotModified, "not modified")
	}
	return nil
}

// EvaluateWrite applies S3 conditional-write semantics (If-Match and
// If-None-Match on PUT and CompleteMultipartUpload). exists reports whether
// a current, non-delete-marker version exists; etag is its ETag. Metadata
// stores must evaluate this atomically with the write.
func (c Conditions) EvaluateWrite(exists bool, etag string) error {
	if c.IfNoneMatch == "*" && exists {
		return s3err.Errorf(s3err.PreconditionFailed, "object already exists")
	}
	if c.IfMatch != "" {
		if !exists {
			return s3err.Errorf(s3err.NoSuchKey, "object does not exist")
		}
		if c.IfMatch != "*" && c.IfMatch != etag {
			return s3err.Errorf(s3err.PreconditionFailed, "If-Match failed")
		}
	}
	return nil
}
