package handlers_test

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// rawResponse sends one request as written and returns the response bytes,
// header names included as the server wrote them.
func rawResponse(t *testing.T, s *server, request string) string {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.addr, ioTimeout)
	must(t, err)
	defer conn.Close()
	must(t, conn.SetDeadline(time.Now().Add(ioTimeout)))
	_, err = io.WriteString(conn, request)
	must(t, err)
	out, err := io.ReadAll(conn)
	must(t, err)
	return string(out)
}

// TestUserMetadataIsSentInLowerCase checks the names of user metadata headers
// on the wire, and that an empty value is still sent. The names are compared as
// bytes because HTTP parsing in Go canonicalizes them.
func TestUserMetadataIsSentInLowerCase(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "meta")
	s.do(t, http.MethodPut, "/meta/k", []byte("x"), http.Header{
		"X-Amz-Meta-Mixed": {"Value"},
		"X-Amz-Meta-Empty": {""},
	})

	resp := rawResponse(t, s, "GET /meta/k HTTP/1.1\r\nHost: test\r\n"+
		"Authorization: AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/s3/aws4_request\r\n"+
		"Connection: close\r\n\r\n")
	if !strings.Contains(resp, "\r\nx-amz-meta-mixed: Value\r\n") {
		t.Fatalf("metadata header not sent in lower case:\n%s", resp)
	}
	if !strings.Contains(resp, "\r\nx-amz-meta-empty: \r\n") {
		t.Fatalf("empty metadata value not sent:\n%s", resp)
	}
}
