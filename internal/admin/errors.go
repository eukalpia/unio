package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

// httpErr is an error with an explicit HTTP status. The S3 status table gives
// the status of most codes; the admin API needs a few that it does not hold,
// such as 401, 413, 415, 429, 501 and the 409 for the root user. It
// implements s3err.Coder, so that s3err.CodeOf reads its code.
type httpErr struct {
	status int
	code   s3err.Code
	msg    string
}

// Error implements error. The text is for logs only.
func (e *httpErr) Error() string { return string(e.code) + ": " + e.msg }

// S3Code implements s3err.Coder.
func (e *httpErr) S3Code() s3err.Code { return e.code }

// RootUserProtected is the code for a change the API refuses to make to the
// root user. It answers 409, as the admin API specification requires.
const RootUserProtected s3err.Code = "RootUserProtected"

// errDenied is the one generic refusal of an authentication or authorization
// failure. It never says which check failed.
func errDenied() error {
	return &httpErr{status: http.StatusForbidden, code: s3err.AccessDenied, msg: "Access Denied"}
}

// errUnauthenticated is returned when no valid session exists: no cookie, a
// tampered or expired cookie, or a user that is gone or no longer an admin.
func errUnauthenticated() error {
	return &httpErr{status: http.StatusUnauthorized, code: s3err.AccessDenied, msg: "Access Denied"}
}

// errBadRequest is a malformed request. msg must not contain client input.
func errBadRequest(msg string) error {
	return &httpErr{status: http.StatusBadRequest, code: s3err.InvalidRequest, msg: msg}
}

// errInvalid is a parameter outside its allowed form. msg must not contain
// client input.
func errInvalid(msg string) error {
	return &httpErr{status: http.StatusBadRequest, code: s3err.InvalidArgument, msg: msg}
}

// errNotFound is a path that names no admin endpoint.
func errNotFound() error {
	return &httpErr{status: http.StatusNotFound, code: s3err.NoSuchEntity, msg: "The requested resource does not exist."}
}

// errMethodNotAllowed is a path that exists but not for this method.
func errMethodNotAllowed() error {
	return &httpErr{status: http.StatusMethodNotAllowed, code: s3err.MethodNotAllowed, msg: "The method is not allowed for this resource."}
}

// errTooLarge is a body longer than MaxBodyBytes.
func errTooLarge() error {
	return &httpErr{status: http.StatusRequestEntityTooLarge, code: s3err.EntityTooLarge, msg: "The request body is larger than the limit."}
}

// errUnsupportedMedia is a body that is not declared as application/json.
func errUnsupportedMedia() error {
	return &httpErr{status: http.StatusUnsupportedMediaType, code: s3err.InvalidRequest, msg: "The request body must be application/json."}
}

// errRootProtected is a change to the root user, which the API refuses.
func errRootProtected() error {
	return &httpErr{status: http.StatusConflict, code: RootUserProtected, msg: "The root user cannot be changed through the admin API."}
}

// errLoginLimited is a login refused by the rate limiter.
func errLoginLimited() error {
	return &httpErr{status: http.StatusTooManyRequests, code: s3err.SlowDown, msg: "Too many login attempts. Try again later."}
}

// errNotImplemented is an endpoint whose feature this node does not provide.
func errNotImplemented(msg string) error {
	return &httpErr{status: http.StatusNotImplemented, code: s3err.NotImplemented, msg: msg}
}

// describe returns the status, code and client message for err. A context
// error and any error without a code are reported by their S3 code.
func describe(err error) (int, s3err.Code, string) {
	var he *httpErr
	if errors.As(err, &he) {
		return he.status, he.code, he.msg
	}
	code := s3err.CodeOf(err)
	return code.HTTPStatus(), code, clientMessage(err, code)
}

// storeFailure reports whether err is a failure of the store or of the
// request's own context, rather than a refusal of the credentials. Such an
// error keeps its own status and is not turned into a denial.
func storeFailure(err error) bool {
	switch s3err.CodeOf(err) {
	case s3err.InternalError, s3err.ServiceUnavailable, s3err.RequestTimeout:
		return true
	}
	return false
}

// authzError maps an error from an authentication or authorization step. A
// store failure keeps its status. Every other failure is the generic denial.
func authzError(err error) error {
	if storeFailure(err) {
		return err
	}
	return errDenied()
}

// clientMessage returns the message sent with code. A message of the same code
// is used when it reads as client text. A message with a package prefix, such
// as "iam: ...", is internal wording, and the fixed text of the code is sent
// instead.
func clientMessage(err error, code s3err.Code) string {
	var se *s3err.Error
	if errors.As(err, &se) && se.Message != "" && se.Code == code && !strings.Contains(se.Message, ": ") {
		return se.Message
	}
	if msg, ok := genericMessages[code]; ok {
		return msg
	}
	return "The request could not be completed."
}

// genericMessages are the fixed client messages for the codes the API sends.
var genericMessages = map[s3err.Code]string{
	s3err.AccessDenied:               "Access Denied",
	s3err.BucketAlreadyExists:        "The requested bucket name is not available.",
	s3err.BucketAlreadyOwnedByYou:    "The bucket already exists.",
	s3err.BucketNotEmpty:             "The bucket you tried to delete is not empty.",
	s3err.ConditionalRequestConflict: "The request conflicts with a concurrent change. Retry the request.",
	s3err.EntityAlreadyExists:        "The entity already exists.",
	s3err.EntityTooLarge:             "The request body is larger than the limit.",
	s3err.InternalError:              "We encountered an internal error. Retry the request.",
	s3err.InvalidArgument:            "A parameter is not valid.",
	s3err.InvalidBucketName:          "The bucket name is not valid.",
	s3err.InvalidRequest:             "The request is not valid.",
	s3err.KeyTooLongError:            "The object key is too long.",
	s3err.MalformedPolicy:            "The policy document is not valid.",
	s3err.MethodNotAllowed:           "The method is not allowed for this resource.",
	s3err.NoSuchBucket:               "The specified bucket does not exist.",
	s3err.NoSuchEntity:               "The requested entity does not exist.",
	s3err.NoSuchKey:                  "The specified key does not exist.",
	s3err.NotImplemented:             "This operation is not implemented.",
	s3err.RequestTimeout:             "The request timed out.",
	s3err.ServiceUnavailable:         "The service is temporarily unavailable. Retry the request.",
}

// errorResponse renders err as the JSON error body with the request id.
func errorResponse(requestID string, err error) Response {
	status, code, msg := describe(err)
	b, _ := json.Marshal(errorBody{Code: string(code), Message: msg, RequestID: requestID})
	h := commonHeader()
	h.Set("Content-Type", "application/json")
	return Response{Status: status, Header: h, Body: b}
}
