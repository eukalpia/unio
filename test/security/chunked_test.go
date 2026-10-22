package security

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	streamingPayload = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	emptySHA256      = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// chunkedUpload builds a signed aws-chunked PUT for data split into chunks of
// chunkSize bytes. mutate, when set, may alter the encoded chunk bodies after
// they were signed.
func (tg target) chunkedUpload(t *testing.T, bucket, key string, data []byte, chunkSize int, mutate func(chunks [][]byte)) *http.Request {
	t.Helper()
	return tg.chunkedUploadDeclaring(t, bucket, key, data, len(data), chunkSize, mutate)
}

// chunkedUploadDeclaring is chunkedUpload with an explicit, correctly signed
// x-amz-decoded-content-length, which may differ from len(data).
func (tg target) chunkedUploadDeclaring(t *testing.T, bucket, key string, data []byte, declared, chunkSize int, mutate func(chunks [][]byte)) *http.Request {
	t.Helper()
	req := newRequest(t, http.MethodPut, tg.objectURL(bucket, key, ""), nil)
	req.Header.Set("Content-Encoding", "aws-chunked")
	req.Header.Set("X-Amz-Decoded-Content-Length", strconv.Itoa(declared))
	now := time.Now().UTC()
	tg.sign(t, req, nil, tg.goodCreds(), now, streamingPayload)

	seed := signatureOf(t, req.Header.Get("Authorization"))
	amzDate := req.Header.Get("X-Amz-Date")
	scope := amzDate[:8] + "/" + tg.region + "/s3/aws4_request"
	key4 := signingKey(tg.secretKey, amzDate[:8], tg.region)

	var chunks [][]byte
	for off := 0; off < len(data); off += chunkSize {
		end := min(off+chunkSize, len(data))
		chunks = append(chunks, bytes.Clone(data[off:end]))
	}
	chunks = append(chunks, nil) // final zero-length chunk

	sigs := make([]string, len(chunks))
	prev := seed
	for i, c := range chunks {
		sum := sha256.Sum256(c)
		sts := strings.Join([]string{"AWS4-HMAC-SHA256-PAYLOAD", amzDate, scope, prev, emptySHA256, hex.EncodeToString(sum[:])}, "\n")
		sigs[i] = hex.EncodeToString(hmacSHA256(key4, []byte(sts)))
		prev = sigs[i]
	}
	if mutate != nil {
		mutate(chunks)
	}

	var body bytes.Buffer
	for i, c := range chunks {
		body.WriteString(strconv.FormatInt(int64(len(c)), 16))
		body.WriteString(";chunk-signature=")
		body.WriteString(sigs[i])
		body.WriteString("\r\n")
		body.Write(c)
		body.WriteString("\r\n")
	}
	encoded := body.Bytes()
	req.Body = readCloser(encoded)
	req.ContentLength = int64(len(encoded))
	return req
}

func signatureOf(t *testing.T, authorization string) string {
	t.Helper()
	_, sig, ok := strings.Cut(authorization, "Signature=")
	if !ok || len(sig) != 64 {
		t.Fatalf("no signature in %q", authorization)
	}
	return sig
}

func signingKey(secret, date, region string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte("s3"))
	return hmacSHA256(k, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func TestChunkedUploadIntegrity(t *testing.T) {
	tg := loadTarget(t)
	bucket := tg.newBucket(t)
	data := randomBytes(t, 3*65536+123)

	t.Run("valid", func(t *testing.T) {
		r := send(t, tg.chunkedUpload(t, bucket, "valid", data, 65536, nil))
		if r.status != http.StatusOK {
			t.Fatalf("valid chunked upload failed: %d %s", r.status, truncate(r.body))
		}
		if !bytes.Equal(tg.getObject(t, bucket, "valid"), data) {
			t.Fatal("stored data differs from the upload")
		}
	})

	t.Run("flipped byte in a middle chunk", func(t *testing.T) {
		req := tg.chunkedUpload(t, bucket, "flipped", data, 65536, func(c [][]byte) { c[1][100] ^= 0x01 })
		expectRejected(t, send(t, req), "SignatureDoesNotMatch")
		if tg.objectExists(t, bucket, "flipped") {
			t.Fatal("a tampered chunk was stored")
		}
	})

	t.Run("chunks reordered", func(t *testing.T) {
		req := tg.chunkedUpload(t, bucket, "reordered", data, 65536, func(c [][]byte) { c[0], c[1] = c[1], c[0] })
		expectRejected(t, send(t, req), "SignatureDoesNotMatch")
		if tg.objectExists(t, bucket, "reordered") {
			t.Fatal("reordered chunks were stored")
		}
	})

	t.Run("final chunk dropped", func(t *testing.T) {
		req := tg.chunkedUpload(t, bucket, "truncated", data, 65536, nil)
		raw := readAllBody(t, req)
		cut := bytes.LastIndex(raw, []byte("0;chunk-signature="))
		req.Body = readCloser(raw[:cut])
		req.ContentLength = int64(cut)
		r := send(t, req)
		expectRejected(t, r)
		if tg.objectExists(t, bucket, "truncated") {
			t.Fatal("an upload without the final chunk was stored")
		}
	})

	t.Run("decoded length lies", func(t *testing.T) {
		// The wrong length is signed, so only the length check can reject it.
		req := tg.chunkedUploadDeclaring(t, bucket, "lying", data, len(data)+10, 65536, nil)
		expectRejected(t, send(t, req))
		if tg.objectExists(t, bucket, "lying") {
			t.Fatal("an upload with a wrong decoded length was stored")
		}
	})
}
