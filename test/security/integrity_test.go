package security

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func readCloser(b []byte) io.ReadCloser { return io.NopCloser(bytes.NewReader(b)) }

func readAllBody(t *testing.T, req *http.Request) []byte {
	t.Helper()
	b, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestContentMD5Mismatch(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	body := []byte("the real payload")
	other := md5.Sum([]byte("something else"))
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "md5", ""), body)
	req.Header.Set("Content-Md5", base64.StdEncoding.EncodeToString(other[:]))
	tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
	expectRejected(t, send(t, req), "BadDigest")
	if tg.objectExists(t, bucket, "md5") {
		t.Fatal("an object with a mismatching Content-MD5 was stored")
	}
}

func TestMalformedContentMD5(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	body := []byte("x")
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "bad-md5", ""), body)
	req.Header.Set("Content-Md5", "not-base64!!")
	tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
	expectRejected(t, send(t, req), "InvalidDigest", "BadDigest")
	if tg.objectExists(t, bucket, "bad-md5") {
		t.Fatal("an object with a malformed Content-MD5 was stored")
	}
}

func TestChecksumHeaderMismatch(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	body := []byte("checksummed payload")
	wrong := crc32.Checksum([]byte("other"), crc32.MakeTable(crc32.Castagnoli))
	enc := base64.StdEncoding.EncodeToString([]byte{byte(wrong >> 24), byte(wrong >> 16), byte(wrong >> 8), byte(wrong)})
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "crc", ""), body)
	req.Header.Set("X-Amz-Checksum-Crc32c", enc)
	tg.sign(t, req, body, tg.goodCreds(), time.Now(), "")
	expectRejected(t, send(t, req), "BadDigest", "XAmzContentChecksumMismatch", "InvalidRequest")
	if tg.objectExists(t, bucket, "crc") {
		t.Fatal("an object with a mismatching checksum was stored")
	}
}

// TestShortBodyIsNotStored sends fewer bytes than Content-Length and closes
// the connection. The partial object must never become visible.
func TestShortBodyIsNotStored(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	full := randomBytes(t, 64<<10)
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "short", ""), full)
	tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "UNSIGNED-PAYLOAD")

	conn := tg.dial(t)
	defer conn.Close()
	w := bufio.NewWriter(conn)
	req.Body = readCloser(full[:1024])
	// Write fails once the short body is exhausted; the bytes already sent
	// are what the server sees.
	_ = req.Write(w)
	_ = w.Flush()
	if c, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = c.CloseWrite()
	}
	_ = conn.SetReadDeadline(time.Now().Add(tg.bodyIdleTimeout + 10*time.Second))
	_, _ = io.Copy(io.Discard, conn)

	if tg.objectExists(t, bucket, "short") {
		t.Fatal("a truncated upload became visible")
	}
}

// dial opens a raw connection to the endpoint, with TLS for https.
func (tg target) dial(t *testing.T) net.Conn {
	t.Helper()
	host := tg.endpoint.Host
	if tg.endpoint.Port() == "" {
		if tg.endpoint.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	if tg.endpoint.Scheme == "https" {
		conn, err := tls.DialWithDialer(d, "tcp", host, &tls.Config{ServerName: tg.endpoint.Hostname(), MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		return conn
	}
	conn, err := d.Dial("tcp", host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

// TestDeclaredTrailerMustArrive covers a Unio guarantee: a trailing checksum
// announced in x-amz-trailer that never arrives fails the upload.
func TestDeclaredTrailerMustArrive(t *testing.T) {
	tg := loadTarget(t)
	tg.unioOnly(t)
	bucket := tg.newBucket(t)
	data := []byte("trailer-protected payload")
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, "trailer", ""), nil)
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("X-Amz-Decoded-Content-Length", "25")
	req.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc32c")
	req.Header.Set("X-Amz-Sdk-Checksum-Algorithm", "CRC32C")
	tg.sign(t, req, nil, tg.goodCreds(), time.Now(), "STREAMING-UNSIGNED-PAYLOAD-TRAILER")
	// One data chunk, the final chunk, and no trailer at all.
	body := []byte("19\r\n" + string(data) + "\r\n0\r\n\r\n")
	req.Body = readCloser(body)
	req.ContentLength = int64(len(body))
	expectRejected(t, send(t, req))
	if tg.objectExists(t, bucket, "trailer") {
		t.Fatal("an upload whose declared trailer never arrived was stored")
	}
}
