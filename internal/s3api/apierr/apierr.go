// Package apierr renders errors as S3 XML error responses. Only the S3 code,
// the status and a client-safe message reach the client. Internal causes are
// logged together with the request id and never written to the response.
package apierr

import (
	"errors"
	"log/slog"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/reqinfo"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
	"github.com/eukalpia/unio/internal/s3err"
)

// contentType is the media type of S3 XML responses.
const contentType = "application/xml"

// Write renders err as the response to the request in c. resource names the
// bucket or object the request targeted, for the Resource element. The status
// comes from the S3 code of err. A 304 response carries no body, and neither
// does a response to HEAD. err must not be nil.
func Write(c *app.RequestContext, logger *slog.Logger, err error, resource string) {
	code := s3err.CodeOf(err)
	info := reqinfo.Get(c)
	if code == s3err.InternalError {
		logger.Error("request failed", "request_id", info.RequestID, "error", err.Error())
	} else if cause := errors.Unwrap(err); cause != nil {
		logger.Warn("request rejected", "request_id", info.RequestID, "code", string(code), "cause", cause.Error())
	}

	status := code.HTTPStatus()
	setCommonHeaders(c, info)
	if status == 304 || string(c.Method()) == "HEAD" {
		c.SetStatusCode(status)
		c.Response.Header.SetContentLength(0)
		return
	}
	resp := xmlenc.ErrorResponse{
		Code:      string(code),
		Message:   clientMessage(err, code),
		Resource:  resource,
		RequestID: info.RequestID,
		HostID:    info.HostID,
	}
	body := xmlenc.AppendError(make([]byte, 0, 256), &resp)
	c.Data(status, contentType, body)
}

// setCommonHeaders sets the headers every S3 response carries.
func setCommonHeaders(c *app.RequestContext, info reqinfo.Info) {
	c.Response.Header.Set("x-amz-request-id", info.RequestID)
	c.Response.Header.Set("x-amz-id-2", info.HostID)
}

// clientMessage returns a message that is safe to show to clients. An
// explicit client message is used when the error carries one; otherwise a
// generic message for the code. Internal text is never used.
func clientMessage(err error, code s3err.Code) string {
	var se *s3err.Error
	if errors.As(err, &se) && se.Message != "" {
		return se.Message
	}
	if msg, ok := genericMessages[code]; ok {
		return msg
	}
	return "The request could not be completed."
}

// Describe returns the S3 code and the client-safe message that Write would
// send for err. It serves per-key errors inside a DeleteObjects response,
// which carry no status of their own.
func Describe(err error) (s3err.Code, string) {
	code := s3err.CodeOf(err)
	return code, clientMessage(err, code)
}
