package s3api_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestShutdownDrainsInFlightRequests starts a large download, begins the
// shutdown while the response is still being written, and keeps reading. The
// download in flight must complete intact, Serve must return only after it,
// new connections must be refused once the shutdown began, and the idle
// keep-alive connection the client keeps afterwards must not hold the
// shutdown. A server that returned at once would let the node close the
// object layer under the request and cut it; one that waited for idle
// connections would take the whole shutdown timeout on every stop.
func TestShutdownDrainsInFlightRequests(t *testing.T) {
	const size = 64 << 20
	srv, err := s3api.New(s3api.Config{Addr: "127.0.0.1:0", Region: "us-east-1", ShutdownTimeout: 20 * time.Second},
		fakes.NewLayer(), &fakes.Authenticator{}, &fakes.Authorizer{}, discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	base := "http://" + addr
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx, ln) }()

	data := patterned(size)
	client := newS3(base, newHTTPClient(t, nil))
	if _, err := client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("drain")}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String("drain"), Key: aws.String("big"), Body: bytes.NewReader(data),
	}); err != nil {
		t.Fatal(err)
	}

	resp := getRaw(t, base, "/drain/big")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got bytes.Buffer
	if _, err := io.CopyN(&got, resp.Body, 1<<20); err != nil {
		t.Fatal(err)
	}

	cancel()
	select {
	case err := <-served:
		t.Fatalf("Serve returned (%v) while a response was still being written", err)
	case <-time.After(200 * time.Millisecond):
	}
	if conn, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		_ = conn.Close()
		t.Error("a new connection was accepted after the shutdown began")
	}

	if _, err := io.Copy(&got, resp.Body); err != nil {
		t.Fatalf("download cut by the shutdown after %d bytes: %v", got.Len(), err)
	}
	if md5.Sum(got.Bytes()) != md5.Sum(data) {
		t.Fatalf("download returned %d bytes that differ from the object", got.Len())
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve returned %v", err)
		}
	case <-time.After(3 * time.Second):
		// The client keeps its connection open for reuse, as SDK pools do;
		// the server must close it rather than wait out the timeout.
		t.Fatal("Serve did not return within 3s of the last response; idle keep-alive connections must be closed")
	}
}
