package apierr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/eukalpia/unio/internal/s3api/reqinfo"
	"github.com/eukalpia/unio/internal/s3err"
)

// secret is a marker that stands for internal detail: a path, a peer address
// or a credential. It must never reach a response body or header.
const secret = "/var/lib/unio/drive7/.unio/SECRET-internal-detail"

// codedError is an s3err.Coder that is not an *s3err.Error and wraps another
// error, as a transport error type may.
type codedError struct {
	code  s3err.Code
	inner error
}

func (e *codedError) Error() string      { return string(e.code) + ": " + e.inner.Error() }
func (e *codedError) Unwrap() error      { return e.inner }
func (e *codedError) S3Code() s3err.Code { return e.code }

// render runs Write for a request with the given method and returns the
// response and the log output.
func render(t *testing.T, method string, err error, resource string) (*app.RequestContext, string) {
	t.Helper()
	c := app.NewContext(0)
	c.Request.Header.SetMethod(method)
	reqinfo.Set(c, reqinfo.Info{RequestID: "REQ1", HostID: "HOST1"})
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	Write(c, logger, err, resource)
	return c, logs.String()
}

// TestWriteNeverLeaksInternalCause checks that the internal cause of an error
// is logged for the operator but never written to the client, for internal
// errors, wrapped client errors and plain errors alike.
func TestWriteNeverLeaksInternalCause(t *testing.T) {
	cause := errors.New("open " + secret + ": input/output error")
	cases := []struct {
		name string
		err  error
		code s3err.Code
	}{
		{"plain error", cause, s3err.InternalError},
		{"wrapped plain error", fmt.Errorf("engine: commit: %w", cause), s3err.InternalError},
		{"internal wrap", s3err.Wrap(s3err.InternalError, cause), s3err.InternalError},
		{"client wrap", s3err.Wrap(s3err.SlowDown, cause), s3err.SlowDown},
		{"sentinel wrapped", fmt.Errorf("%s: %w", secret, s3err.NewSentinel("drive offline", s3err.ServiceUnavailable)), s3err.ServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, logs := render(t, "GET", tc.err, "/bucket/key")
			body := string(c.Response.Body())
			if strings.Contains(body, "SECRET") || strings.Contains(body, "/var/lib") {
				t.Fatalf("response body leaks the internal cause:\n%s", body)
			}
			c.Response.Header.VisitAll(func(k, v []byte) {
				if bytes.Contains(v, []byte("SECRET")) {
					t.Fatalf("header %s leaks the internal cause: %s", k, v)
				}
			})
			if got := c.Response.StatusCode(); got != tc.code.HTTPStatus() {
				t.Fatalf("status = %d, want %d", got, tc.code.HTTPStatus())
			}
			if !strings.Contains(body, "<Code>"+string(tc.code)+"</Code>") {
				t.Fatalf("body does not carry code %s:\n%s", tc.code, body)
			}
			if !strings.Contains(logs, "SECRET") || !strings.Contains(logs, "REQ1") {
				t.Fatalf("operator log lacks the cause or the request id:\n%s", logs)
			}
		})
	}
}

// TestWriteUsesOnlyMatchingMessage checks that an explicit client message is
// shown only when it belongs to the reported code. A message of an inner
// error with another code must not be paired with the outer code.
func TestWriteUsesOnlyMatchingMessage(t *testing.T) {
	explicit := s3err.Errorf(s3err.InvalidArgument, "max-keys must be an integer between 0 and 1000")
	c, _ := render(t, "GET", explicit, "/b")
	if body := string(c.Response.Body()); !strings.Contains(body, "<Message>max-keys must be an integer between 0 and 1000</Message>") {
		t.Fatalf("explicit message missing:\n%s", body)
	}

	outer := &codedError{code: s3err.ServiceUnavailable, inner: s3err.Errorf(s3err.InvalidArgument, "inner message")}
	c, _ = render(t, "GET", outer, "/b")
	body := string(c.Response.Body())
	if strings.Contains(body, "inner message") {
		t.Fatalf("message of another code was shown:\n%s", body)
	}
	if !strings.Contains(body, "<Message>"+genericMessages[s3err.ServiceUnavailable]+"</Message>") {
		t.Fatalf("generic message of the reported code missing:\n%s", body)
	}
}

// TestWriteEscapesReflectedValues checks that client-controlled text that is
// reflected in an error response (the resource, explicit messages) cannot
// break out of its element or produce an ill-formed document.
func TestWriteEscapesReflectedValues(t *testing.T) {
	resource := "/b/</Resource><Code>AccessDenied</Code>&\x00\x01\xff<script>"
	c, _ := render(t, "GET", s3err.Errorf(s3err.NoSuchKey, "key %q", "<x>&"), resource)
	body := string(c.Response.Body())
	if strings.Count(body, "<Code>") != 1 || strings.Contains(body, "<script>") || strings.Contains(body, "<x>") {
		t.Fatalf("reflected value broke out of its element:\n%s", body)
	}
	for _, b := range []byte{0x00, 0x01, 0xff} {
		if strings.IndexByte(body, b) >= 0 {
			t.Fatalf("body contains byte %#x that XML 1.0 forbids:\n%q", b, body)
		}
	}
}

// TestWriteWithoutBody checks that 304 responses and responses to HEAD carry
// the status and the request ids, but no body.
func TestWriteWithoutBody(t *testing.T) {
	for _, tc := range []struct {
		method string
		err    error
		status int
	}{
		{"HEAD", s3err.Errorf(s3err.NoSuchKey, "gone"), 404},
		{"HEAD", errors.New(secret), 500},
		{"GET", s3err.Errorf(s3err.NotModified, ""), 304},
	} {
		c, _ := render(t, tc.method, tc.err, "/b/k")
		if got := c.Response.StatusCode(); got != tc.status {
			t.Fatalf("%s: status = %d, want %d", tc.method, got, tc.status)
		}
		if n := len(c.Response.Body()); n != 0 {
			t.Fatalf("%s %d: body has %d bytes, want none", tc.method, tc.status, n)
		}
		if got := string(c.Response.Header.Peek("x-amz-request-id")); got != "REQ1" {
			t.Fatalf("x-amz-request-id = %q", got)
		}
		if got := string(c.Response.Header.Peek("x-amz-id-2")); got != "HOST1" {
			t.Fatalf("x-amz-id-2 = %q", got)
		}
	}
}

// TestContextErrors checks that a cancelled or expired request reports
// RequestTimeout rather than an internal error.
func TestContextErrors(t *testing.T) {
	for _, err := range []error{context.Canceled, fmt.Errorf("read: %w", context.DeadlineExceeded)} {
		code, msg := Describe(err)
		if code != s3err.RequestTimeout || msg != genericMessages[s3err.RequestTimeout] {
			t.Fatalf("Describe(%v) = %s, %q", err, code, msg)
		}
	}
}

// TestDescribe checks the per-key form used inside DeleteObjects.
func TestDescribe(t *testing.T) {
	code, msg := Describe(s3err.Wrap(s3err.AccessDenied, errors.New(secret)))
	if code != s3err.AccessDenied || msg != "Access Denied" {
		t.Fatalf("Describe = %s, %q", code, msg)
	}
	code, msg = Describe(errors.New(secret))
	if code != s3err.InternalError || strings.Contains(msg, "SECRET") {
		t.Fatalf("Describe(internal) = %s, %q", code, msg)
	}
}

// TestEveryCodeHasGenericMessage reads the S3 code constants from the s3err
// sources, so that a code added later without a client-safe message, or with
// an implicit 500 status, fails here instead of reaching clients as a bare
// "could not be completed".
func TestEveryCodeHasGenericMessage(t *testing.T) {
	codes := s3errCodes(t)
	if len(codes) < 40 {
		t.Fatalf("parsed only %d codes; the parser no longer matches codes.go", len(codes))
	}
	for name, value := range codes {
		code := s3err.Code(value)
		msg, ok := genericMessages[code]
		if !ok || msg == "" {
			t.Errorf("s3err.%s (%s) has no generic client message", name, value)
		}
		if code != s3err.InternalError && code.HTTPStatus() == 500 {
			t.Errorf("s3err.%s (%s) has no explicit HTTP status", name, value)
		}
	}
	for code := range genericMessages {
		found := false
		for _, v := range codes {
			if s3err.Code(v) == code {
				found = true
			}
		}
		if !found {
			t.Errorf("generic message for unknown code %s", code)
		}
	}
}

// s3errCodes returns the constants of type Code declared in s3err/codes.go,
// by name and value.
func s3errCodes(t *testing.T) map[string]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "../../s3err/codes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	codes := make(map[string]string)
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Code" {
				continue
			}
			for i, name := range vs.Names {
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok {
					t.Fatalf("code %s has a non-literal value", name.Name)
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				codes[name.Name] = v
			}
		}
	}
	return codes
}
