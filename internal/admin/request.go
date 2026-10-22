package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/eukalpia/unio/internal/s3err"
)

// BasePath is the URL path prefix of the admin API.
const BasePath = "/unio/admin/v1"

// MaxBodyBytes bounds a request body. A body that is longer is refused with
// 413 before any of it is decoded.
const MaxBodyBytes = 64 << 10

// Request is one admin request, as the S3 front end hands it over.
type Request struct {
	// Method is the HTTP method.
	Method string
	// Path is the raw request path, beginning with BasePath, exactly as sent.
	Path string
	// Query is the raw query string, without the leading "?".
	Query string
	// Host is the Host header value, which may include a port.
	Host string
	// Header holds the request headers under canonical keys.
	Header http.Header
	// Body is the request body, or nil when there is none. The service reads
	// it at most once and at most MaxBodyBytes of it.
	Body io.Reader
	// ContentLength is the declared body length, or -1 when unknown.
	ContentLength int64
	// RemoteAddr is the client address as host:port.
	RemoteAddr string
	// RequestID identifies the request in responses and log lines.
	RequestID string
}

// Response is the answer to one admin request.
type Response struct {
	// Status is the HTTP status code.
	Status int
	// Header holds the response headers. Set-Cookie may appear more than once.
	Header http.Header
	// Body is the response body, empty for 204.
	Body []byte
}

// errorBody is the JSON form of every error response.
type errorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

// readBody reads the request body, at most MaxBodyBytes of it. A body longer
// than that is 413. A read that fails because the payload did not verify
// (a digest or chunk signature mismatch) is AccessDenied, as every
// authentication failure is. Any other read failure is a bad request.
func readBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxBodyBytes+1))
	if err != nil {
		var coder s3err.Coder
		if errors.As(err, &coder) {
			return nil, errDenied()
		}
		return nil, errBadRequest("The request body could not be read.")
	}
	if len(b) > MaxBodyBytes {
		return nil, errTooLarge()
	}
	return b, nil
}

// decodeBody decodes the JSON body of a request into dst. The body must be
// present, must be declared application/json, must hold exactly one JSON
// value, and may contain no field that dst does not declare.
func decodeBody(c *call, dst any) error {
	if len(c.body) == 0 {
		return errBadRequest("The request body is required.")
	}
	mt, _, err := mime.ParseMediaType(c.req.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return errUnsupportedMedia()
	}
	return decodeJSON(c.body, dst)
}

// decodeJSON decodes exactly one JSON value from b into dst, refusing unknown
// fields. The error never names the field.
func decodeJSON(b []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errBadRequest("The request body is not valid for this request.")
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errBadRequest("The request body must hold one JSON value.")
	}
	return nil
}

// jsonResponse returns a response with status and the JSON encoding of v.
func jsonResponse(status int, v any) (Response, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Response{}, err
	}
	h := commonHeader()
	h.Set("Content-Type", "application/json")
	return Response{Status: status, Header: h, Body: b}, nil
}

// emptyResponse returns a response with status and no body.
func emptyResponse(status int) Response {
	return Response{Status: status, Header: commonHeader()}
}

// commonHeader returns the headers every admin response carries. Nothing the
// API returns may be cached, and a browser must not sniff a type.
func commonHeader() http.Header {
	h := make(http.Header, 4)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	return h
}
