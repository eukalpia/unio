package s3api_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/eukalpia/unio/internal/s3api"
)

// seedObject stores data as bucket/key through the SDK and returns the base URL.
func seedObject(t testing.TB, cfg s3api.Config, bucket, key string, data []byte) string {
	t.Helper()
	base := startServer(t, cfg, nil)
	client := newS3(base, newHTTPClient(t, nil))
	ctx := context.Background()
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data)}); err != nil {
		t.Fatal(err)
	}
	return base
}

// getRaw starts a GET and returns the response with the body still unread.
func getRaw(t testing.TB, base, path string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "x")
	resp, err := newHTTPClient(t, nil).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// TestSlowDownloadIsNotCut keeps reading a download whose total duration is
// far longer than the write idle timeout. Each pause is shorter than the idle
// timeout, so the server must keep the connection, and the bytes must arrive
// intact.
//
// The idle timeout bounds the wait for one write to be accepted by the
// kernel, and Linux wakes a writer blocked on a full socket only once about a
// third of the send buffer is free, which on loopback is megabytes. A steady
// client must therefore drain that much within one idle period. The timeout
// here leaves room for that even under the race detector; the production
// default of 60 s asks a client for a few tens of kilobytes per second.
func TestSlowDownloadIsNotCut(t *testing.T) {
	const size = 12 << 20
	data := patterned(size)
	cfg := s3api.Config{WriteIdleTimeout: 2 * time.Second}
	base := seedObject(t, cfg, "slow", "obj", data)

	resp := getRaw(t, base, "/slow/obj")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	started := time.Now()
	var got bytes.Buffer
	buf := make([]byte, 64<<10)
	for {
		n, err := resp.Body.Read(buf)
		got.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("slow download failed after %d bytes: %v", got.Len(), err)
		}
		time.Sleep(20 * time.Millisecond) // a slow but steady client
	}
	if elapsed := time.Since(started); elapsed <= cfg.WriteIdleTimeout {
		t.Fatalf("download took %v, want longer than the idle timeout so the test proves idleness", elapsed)
	}
	if md5.Sum(got.Bytes()) != md5.Sum(data) {
		t.Fatalf("slow download returned %d bytes that differ from the object", got.Len())
	}
}

// TestStalledDownloadIsCut stops reading a large download for longer than the
// write idle timeout. The server must give up on the client, so the rest of
// the body never arrives and the response is truncated.
func TestStalledDownloadIsCut(t *testing.T) {
	const size = 32 << 20
	cfg := s3api.Config{WriteIdleTimeout: 200 * time.Millisecond}
	base := seedObject(t, cfg, "stall", "obj", patterned(size))

	resp := getRaw(t, base, "/stall/obj")
	head := make([]byte, 4<<10)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // the client stalls for five idle periods

	n, err := io.Copy(io.Discard, resp.Body)
	total := int64(len(head)) + n
	if err == nil || total >= size {
		t.Fatalf("stalled download completed (%d bytes, err %v); the server should have cut it", total, err)
	}
}

// TestWriteCapCutsLongDownload checks the optional overall cap. The client keeps
// reading steadily, so the idle timeout never fires, but the response is
// longer than WriteTimeout and must be cut when the cap passes.
func TestWriteCapCutsLongDownload(t *testing.T) {
	const size = 32 << 20
	cfg := s3api.Config{WriteIdleTimeout: time.Second, WriteTimeout: 300 * time.Millisecond}
	base := seedObject(t, cfg, "capped", "obj", patterned(size))

	resp := getRaw(t, base, "/capped/obj")
	var got int64
	buf := make([]byte, 1<<20)
	var readErr error
	for {
		n, err := resp.Body.Read(buf)
		got += int64(n)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if readErr == nil || got >= size {
		t.Fatalf("download of %d bytes completed past the overall cap (err %v)", got, readErr)
	}
}
