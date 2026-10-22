package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// ErrorCode is an S3-compatible error code. Codes are what clients see;
// everything else about an internal error stays on the server.
type ErrorCode string

// S3 error codes used by Unio.
const (
	ErrAccessDenied                       ErrorCode = "AccessDenied"
	ErrAuthorizationHeaderMalformed       ErrorCode = "AuthorizationHeaderMalformed"
	ErrBadDigest                          ErrorCode = "BadDigest"
	ErrBucketAlreadyExists                ErrorCode = "BucketAlreadyExists"
	ErrBucketAlreadyOwnedByYou            ErrorCode = "BucketAlreadyOwnedByYou"
	ErrBucketNotEmpty                     ErrorCode = "BucketNotEmpty"
	ErrEntityTooLarge                     ErrorCode = "EntityTooLarge"
	ErrEntityTooSmall                     ErrorCode = "EntityTooSmall"
	ErrExpiredToken                       ErrorCode = "ExpiredToken"
	ErrIncompleteBody                     ErrorCode = "IncompleteBody"
	ErrInternalError                      ErrorCode = "InternalError"
	ErrInvalidAccessKeyID                 ErrorCode = "InvalidAccessKeyId"
	ErrInvalidArgument                    ErrorCode = "InvalidArgument"
	ErrInvalidBucketName                  ErrorCode = "InvalidBucketName"
	ErrInvalidDigest                      ErrorCode = "InvalidDigest"
	ErrInvalidPart                        ErrorCode = "InvalidPart"
	ErrInvalidPartOrder                   ErrorCode = "InvalidPartOrder"
	ErrInvalidRange                       ErrorCode = "InvalidRange"
	ErrInvalidRequest                     ErrorCode = "InvalidRequest"
	ErrKeyTooLongError                    ErrorCode = "KeyTooLongError"
	ErrMalformedXML                       ErrorCode = "MalformedXML"
	ErrMetadataTooLarge                   ErrorCode = "MetadataTooLarge"
	ErrMethodNotAllowed                   ErrorCode = "MethodNotAllowed"
	ErrMissingContentLength               ErrorCode = "MissingContentLength"
	ErrNoSuchBucket                       ErrorCode = "NoSuchBucket"
	ErrNoSuchKey                          ErrorCode = "NoSuchKey"
	ErrNoSuchUpload                       ErrorCode = "NoSuchUpload"
	ErrNoSuchVersion                      ErrorCode = "NoSuchVersion"
	ErrNotImplemented                     ErrorCode = "NotImplemented"
	ErrNotModified                        ErrorCode = "NotModified"
	ErrPreconditionFailed                 ErrorCode = "PreconditionFailed"
	ErrConditionalRequestConflict         ErrorCode = "ConditionalRequestConflict"
	ErrRequestTimeTooSkewed               ErrorCode = "RequestTimeTooSkewed"
	ErrRequestTimeout                     ErrorCode = "RequestTimeout"
	ErrServiceUnavailable                 ErrorCode = "ServiceUnavailable"
	ErrSignatureDoesNotMatch              ErrorCode = "SignatureDoesNotMatch"
	ErrSlowDown                           ErrorCode = "SlowDown"
	ErrXAmzContentSHA256Mismatch          ErrorCode = "XAmzContentSHA256Mismatch"
	ErrXAmzContentChecksumMismatch        ErrorCode = "XAmzContentChecksumMismatch"
	ErrInvalidChecksum                    ErrorCode = "InvalidChecksum"
	ErrMissingSecurityHeader              ErrorCode = "MissingSecurityHeader"
	ErrInvalidStorageClass                ErrorCode = "InvalidStorageClass"
	ErrRequestHeaderSectionTooLarge       ErrorCode = "RequestHeaderSectionTooLarge"
	ErrMaxMessageLengthExceeded           ErrorCode = "MaxMessageLengthExceeded"
	ErrInvalidObjectState                 ErrorCode = "InvalidObjectState"
	ErrUnsupportedSignatureVersion        ErrorCode = "UnsupportedSignature"
	ErrInvalidTag                         ErrorCode = "InvalidTag"
	ErrInvalidLocationConstraint          ErrorCode = "InvalidLocationConstraint"
	ErrIllegalLocationConstraintException ErrorCode = "IllegalLocationConstraintException"
)

var httpStatus = map[ErrorCode]int{
	ErrAccessDenied:                       http.StatusForbidden,
	ErrAuthorizationHeaderMalformed:       http.StatusBadRequest,
	ErrBadDigest:                          http.StatusBadRequest,
	ErrBucketAlreadyExists:                http.StatusConflict,
	ErrBucketAlreadyOwnedByYou:            http.StatusConflict,
	ErrBucketNotEmpty:                     http.StatusConflict,
	ErrEntityTooLarge:                     http.StatusBadRequest,
	ErrEntityTooSmall:                     http.StatusBadRequest,
	ErrExpiredToken:                       http.StatusBadRequest,
	ErrIncompleteBody:                     http.StatusBadRequest,
	ErrInternalError:                      http.StatusInternalServerError,
	ErrInvalidAccessKeyID:                 http.StatusForbidden,
	ErrInvalidArgument:                    http.StatusBadRequest,
	ErrInvalidBucketName:                  http.StatusBadRequest,
	ErrInvalidDigest:                      http.StatusBadRequest,
	ErrInvalidPart:                        http.StatusBadRequest,
	ErrInvalidPartOrder:                   http.StatusBadRequest,
	ErrInvalidRange:                       http.StatusRequestedRangeNotSatisfiable,
	ErrInvalidRequest:                     http.StatusBadRequest,
	ErrKeyTooLongError:                    http.StatusBadRequest,
	ErrMalformedXML:                       http.StatusBadRequest,
	ErrMetadataTooLarge:                   http.StatusBadRequest,
	ErrMethodNotAllowed:                   http.StatusMethodNotAllowed,
	ErrMissingContentLength:               http.StatusLengthRequired,
	ErrNoSuchBucket:                       http.StatusNotFound,
	ErrNoSuchKey:                          http.StatusNotFound,
	ErrNoSuchUpload:                       http.StatusNotFound,
	ErrNoSuchVersion:                      http.StatusNotFound,
	ErrNotImplemented:                     http.StatusNotImplemented,
	ErrNotModified:                        http.StatusNotModified,
	ErrPreconditionFailed:                 http.StatusPreconditionFailed,
	ErrConditionalRequestConflict:         http.StatusConflict,
	ErrRequestTimeTooSkewed:               http.StatusForbidden,
	ErrRequestTimeout:                     http.StatusBadRequest,
	ErrServiceUnavailable:                 http.StatusServiceUnavailable,
	ErrSignatureDoesNotMatch:              http.StatusForbidden,
	ErrSlowDown:                           http.StatusServiceUnavailable,
	ErrXAmzContentSHA256Mismatch:          http.StatusBadRequest,
	ErrXAmzContentChecksumMismatch:        http.StatusBadRequest,
	ErrInvalidChecksum:                    http.StatusBadRequest,
	ErrMissingSecurityHeader:              http.StatusBadRequest,
	ErrInvalidStorageClass:                http.StatusBadRequest,
	ErrRequestHeaderSectionTooLarge:       http.StatusBadRequest,
	ErrMaxMessageLengthExceeded:           http.StatusBadRequest,
	ErrInvalidObjectState:                 http.StatusForbidden,
	ErrUnsupportedSignatureVersion:        http.StatusBadRequest,
	ErrInvalidTag:                         http.StatusBadRequest,
	ErrInvalidLocationConstraint:          http.StatusBadRequest,
	ErrIllegalLocationConstraintException: http.StatusBadRequest,
}

// HTTPStatus returns the HTTP status code S3 uses for c. Unknown codes map
// to 500.
func (c ErrorCode) HTTPStatus() int {
	if s, ok := httpStatus[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error is a typed error carrying an S3 error code. Message is safe to show
// to clients. Err is the internal cause; it is logged, never returned to
// clients.
type Error struct {
	Code    ErrorCode
	Message string
	Err     error
}

// Errorf returns an *Error with the given code and a client-safe message.
func Errorf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap returns an *Error with the given code wrapping an internal cause.
// The message shown to clients is the generic one for the code.
func Wrap(code ErrorCode, err error) *Error {
	return &Error{Code: code, Err: err}
}

func (e *Error) Error() string {
	switch {
	case e.Message != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	case e.Message != "":
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	default:
		return string(e.Code)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is an *Error with the same code, so callers can
// write errors.Is(err, core.Errorf(core.ErrNoSuchKey, "")) or use IsCode.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// Internal sentinel errors. They never reach clients directly; CodeOf maps
// them to S3 codes.
var (
	// ErrShardNotFound is returned by a Drive for a shard it does not hold.
	ErrShardNotFound = errors.New("core: shard not found")
	// ErrShardExists is returned by a Drive when writing a shard id that
	// already exists. Shards are immutable.
	ErrShardExists = errors.New("core: shard already exists")
	// ErrDriveOffline is returned when a drive cannot be reached.
	ErrDriveOffline = errors.New("core: drive offline")
	// ErrDriveFaulty is returned when a drive reports I/O errors.
	ErrDriveFaulty = errors.New("core: drive faulty")
	// ErrCorrupt reports data that failed integrity verification.
	ErrCorrupt = errors.New("core: data corrupt")
	// ErrWriteQuorum is returned when fewer drives than the write quorum
	// acknowledged a write.
	ErrWriteQuorum = errors.New("core: write quorum not reached")
	// ErrReadQuorum is returned when fewer shards than needed could be read.
	ErrReadQuorum = errors.New("core: read quorum not reached")
	// ErrNotLeader is returned by a metadata replica that is not the leader
	// of its group.
	ErrNotLeader = errors.New("core: not the leader")
	// ErrRevisionMismatch is returned by compare-and-swap operations.
	ErrRevisionMismatch = errors.New("core: revision mismatch")
	// ErrNotFound is a generic not-found for internal key-value lookups.
	ErrNotFound = errors.New("core: not found")
	// ErrClosed is returned by components used after Close.
	ErrClosed = errors.New("core: closed")
)

// CodeOf returns the S3 error code for err. Errors without a code map to
// InternalError, except quorum and availability errors (ServiceUnavailable)
// and context errors (RequestTimeout).
func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	switch {
	case errors.Is(err, ErrWriteQuorum), errors.Is(err, ErrReadQuorum),
		errors.Is(err, ErrNotLeader), errors.Is(err, ErrDriveOffline):
		return ErrServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ErrRequestTimeout
	}
	return ErrInternalError
}

// IsCode reports whether err carries the given S3 error code.
func IsCode(err error, code ErrorCode) bool { return err != nil && CodeOf(err) == code }
