package handlers_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestMain verifies that no goroutine outlives the tests. Hertz starts one
// process-wide goroutine that refreshes the Date header and never exits; it is
// ignored by name, because it is a library singleton and not a leak.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreAnyFunction("github.com/cloudwego/hertz/pkg/protocol.UpdateServerDate.func1"))
}

const (
	// ioTimeout bounds every raw network exchange, so that a server which
	// waits for bytes that never arrive fails the test instead of hanging it.
	ioTimeout = 5 * time.Second
	// requestTimeout bounds one request sent through the HTTP client.
	requestTimeout = 30 * time.Second
)

// server is an S3 front end running on a random localhost port.
type server struct {
	// addr is the host:port the server listens on.
	addr string
	// client sends the requests of a test. Its transport is closed at cleanup.
	client *http.Client
}

// reply is one complete HTTP response, with its body read in full.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// s3Error is the part of an S3 error document that the tests check.
type s3Error struct {
	Code string `xml:"Code"`
}

// uploadResult is the part of an InitiateMultipartUploadResult document that
// the tests need.
type uploadResult struct {
	UploadID string `xml:"UploadId"`
}

// newServer starts an S3 front end on a random localhost port. It is backed by
// an empty in-memory layer and by authz, and it is stopped when the test ends.
func newServer(t *testing.T, authz auth.Authorizer) *server {
	t.Helper()
	return newServerWithLayer(t, fakes.NewLayer(), authz)
}

// newServerWithLayer is newServer over the given layer.
func newServerWithLayer(t *testing.T, layer object.Layer, authz auth.Authorizer) *server {
	t.Helper()
	srv, err := s3api.New(s3api.Config{Addr: "127.0.0.1:0", Region: "us-east-1"},
		layer, &fakes.Authenticator{}, authz,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln) }()

	transport := &http.Transport{}
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Errorf("serve returned: %v", err)
		}
		transport.CloseIdleConnections()
	})
	return &server{
		addr:   ln.Addr().String(),
		client: &http.Client{Transport: transport, Timeout: requestTimeout},
	}
}

// do sends one request and reads the whole response. Every request carries an
// Authorization header, which the fake authenticator reports as signed.
func (s *server) do(t *testing.T, method, target string, body []byte, header http.Header) reply {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+s.addr+target, bytes.NewReader(body))
	must(t, err)
	maps.Copy(req.Header, header)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=test/20260101/us-east-1/s3/aws4_request")
	resp, err := s.client.Do(req)
	must(t, err)
	defer resp.Body.Close()
	return readReply(t, resp)
}

// raw writes request exactly as given on a new connection and reads one
// response. The caller controls the framing headers and the body bytes, which
// the HTTP client would otherwise compute.
func (s *server) raw(t *testing.T, request string) reply {
	t.Helper()
	conn, err := net.DialTimeout("tcp", s.addr, ioTimeout)
	must(t, err)
	defer conn.Close()
	must(t, conn.SetDeadline(time.Now().Add(ioTimeout)))
	_, err = io.WriteString(conn, request)
	must(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	must(t, err)
	defer resp.Body.Close()
	return readReply(t, resp)
}

// createBucket creates a bucket and fails the test unless the request succeeds.
func (s *server) createBucket(t *testing.T, name string) {
	t.Helper()
	r := s.do(t, http.MethodPut, "/"+name, nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("create bucket %s: status %d, body %s", name, r.status, r.body)
	}
}

// startUpload starts a multipart upload of bucket/key and returns its id.
func (s *server) startUpload(t *testing.T, bucket, key string) string {
	t.Helper()
	r := s.do(t, http.MethodPost, "/"+bucket+"/"+key+"?uploads", nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("create upload: status %d, body %s", r.status, r.body)
	}
	var res uploadResult
	if err := xml.Unmarshal(r.body, &res); err != nil {
		t.Fatalf("create upload: bad response: %v\n%s", err, r.body)
	}
	if res.UploadID == "" {
		t.Fatalf("create upload: no UploadId in %s", r.body)
	}
	return res.UploadID
}

// assertAbsent checks that bucket/key holds no object, by a GET that must fail
// with NoSuchKey.
func (s *server) assertAbsent(t *testing.T, bucket, key string) {
	t.Helper()
	r := s.do(t, http.MethodGet, "/"+bucket+"/"+key, nil, nil)
	expectError(t, r, http.StatusNotFound, "NoSuchKey")
}

// expectError checks the HTTP status and the S3 error code of r. An empty code
// means that the body must hold no error document.
func expectError(t *testing.T, r reply, status int, code string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("status = %d, want %d; body %s", r.status, status, r.body)
	}
	if got := errorCode(t, r.body); got != code {
		t.Fatalf("code = %q, want %q; body %s", got, code, r.body)
	}
}

// errorCode returns the Code element of an S3 error document, or "" when the
// body is empty.
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	if len(body) == 0 {
		return ""
	}
	var e s3Error
	if err := xml.Unmarshal(body, &e); err != nil {
		t.Fatalf("error body is not XML: %v\n%s", err, body)
	}
	return e.Code
}

// hdr builds an http.Header from name and value pairs. Names are canonicalized
// as http.Header.Set does.
func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

// readReply reads the body of resp and returns the complete response.
func readReply(t *testing.T, resp *http.Response) reply {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	must(t, err)
	return reply{status: resp.StatusCode, header: resp.Header, body: b}
}

// must fails the test when err is not nil.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
