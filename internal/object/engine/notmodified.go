package engine

import (
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// notModifiedCause is the cause of the NotModified error that a read returns
// when its conditions hold. RFC 9110 requires the 304 response to carry the
// ETag and Last-Modified of the object, so the cause exposes them through
// NotModifiedInfo. A front end that needs them declares a local interface and
// calls errors.As, as it does for deleteMarkerCause.
type notModifiedCause struct {
	info object.Info
}

// Error implements error.
func (c notModifiedCause) Error() string {
	return "the object has not been modified"
}

// NotModifiedInfo returns the description of the object that was not modified.
func (c notModifiedCause) NotModifiedInfo() object.Info {
	return c.info
}

// evaluateRead applies the read conditions to info. A NotModified outcome keeps
// the object description in its cause, so that the 304 can carry its validators.
func evaluateRead(conds object.Conditions, info object.Info) error {
	err := conds.EvaluateRead(info)
	if s3err.Is(err, s3err.NotModified) {
		return s3err.Wrap(s3err.NotModified, notModifiedCause{info: info})
	}
	return err
}
