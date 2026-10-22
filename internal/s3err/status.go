package s3err

import "net/http"

var httpStatus = map[Code]int{
	AccessDenied:                       http.StatusForbidden,
	AuthorizationHeaderMalformed:       http.StatusBadRequest,
	BadDigest:                          http.StatusBadRequest,
	BucketAlreadyExists:                http.StatusConflict,
	BucketAlreadyOwnedByYou:            http.StatusConflict,
	BucketNotEmpty:                     http.StatusConflict,
	ConditionalRequestConflict:         http.StatusConflict,
	EntityTooLarge:                     http.StatusBadRequest,
	EntityTooSmall:                     http.StatusBadRequest,
	ExpiredToken:                       http.StatusBadRequest,
	IllegalLocationConstraintException: http.StatusBadRequest,
	IncompleteBody:                     http.StatusBadRequest,
	InternalError:                      http.StatusInternalServerError,
	InvalidAccessKeyID:                 http.StatusForbidden,
	InvalidArgument:                    http.StatusBadRequest,
	InvalidBucketName:                  http.StatusBadRequest,
	InvalidChecksum:                    http.StatusBadRequest,
	InvalidDigest:                      http.StatusBadRequest,
	InvalidLocationConstraint:          http.StatusBadRequest,
	InvalidObjectState:                 http.StatusForbidden,
	InvalidPart:                        http.StatusBadRequest,
	InvalidPartOrder:                   http.StatusBadRequest,
	InvalidRange:                       http.StatusRequestedRangeNotSatisfiable,
	InvalidRequest:                     http.StatusBadRequest,
	InvalidStorageClass:                http.StatusBadRequest,
	InvalidTag:                         http.StatusBadRequest,
	KeyTooLongError:                    http.StatusBadRequest,
	MalformedXML:                       http.StatusBadRequest,
	MaxMessageLengthExceeded:           http.StatusBadRequest,
	MetadataTooLarge:                   http.StatusBadRequest,
	MethodNotAllowed:                   http.StatusMethodNotAllowed,
	MissingContentLength:               http.StatusLengthRequired,
	MissingSecurityHeader:              http.StatusBadRequest,
	NoSuchBucket:                       http.StatusNotFound,
	NoSuchKey:                          http.StatusNotFound,
	NoSuchUpload:                       http.StatusNotFound,
	NoSuchVersion:                      http.StatusNotFound,
	NotImplemented:                     http.StatusNotImplemented,
	NotModified:                        http.StatusNotModified,
	PreconditionFailed:                 http.StatusPreconditionFailed,
	RequestHeaderSectionTooLarge:       http.StatusBadRequest,
	RequestTimeTooSkewed:               http.StatusForbidden,
	RequestTimeout:                     http.StatusBadRequest,
	ServiceUnavailable:                 http.StatusServiceUnavailable,
	SignatureDoesNotMatch:              http.StatusForbidden,
	SlowDown:                           http.StatusServiceUnavailable,
	UnsupportedSignature:               http.StatusBadRequest,
	XAmzContentChecksumMismatch:        http.StatusBadRequest,
	XAmzContentSHA256Mismatch:          http.StatusBadRequest,
}

// HTTPStatus returns the HTTP status code S3 uses for c. Unknown codes map
// to 500.
func (c Code) HTTPStatus() int {
	if s, ok := httpStatus[c]; ok {
		return s
	}
	return http.StatusInternalServerError
}
