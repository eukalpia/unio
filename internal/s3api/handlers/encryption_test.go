package handlers_test

import (
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
	"net/http"
	"testing"
)

// encryptionRequests are the request headers that ask for server-side
// encryption: SSE-S3, SSE-KMS with its key, context and bucket-key options,
// SSE-C with the customer key, and the copy-source form of SSE-C. This server
// stores objects without encryption, so each of them must be refused rather
// than silently stored in clear.
var encryptionRequests = []struct {
	name   string
	header http.Header
}{
	{"SSE-S3", hdr("x-amz-server-side-encryption", "AES256")},
	{"SSE-KMS", hdr(
		"x-amz-server-side-encryption", "aws:kms",
		"x-amz-server-side-encryption-aws-kms-key-id", "arn:aws:kms:us-east-1:111122223333:key/1",
		"x-amz-server-side-encryption-context", "eyJhIjoiYiJ9",
		"x-amz-server-side-encryption-bucket-key-enabled", "true",
	)},
	{"SSE-C", hdr(
		"x-amz-server-side-encryption-customer-algorithm", "AES256",
		"x-amz-server-side-encryption-customer-key", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"x-amz-server-side-encryption-customer-key-md5", "ZmFrZW1kNWZha2VtZDVmYWtlbWQ1Zg==",
	)},
	{"copy-source SSE-C", hdr(
		"x-amz-copy-source-server-side-encryption-customer-algorithm", "AES256",
		"x-amz-copy-source-server-side-encryption-customer-key", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		"x-amz-copy-source-server-side-encryption-customer-key-md5", "ZmFrZW1kNWZha2VtZDVmYWtlbWQ1Zg==",
	)},
}

// TestEncryptionRequestsAreRefusedOnWrites checks that each write operation
// refuses every encryption request with NotImplemented and stores nothing. A
// plain request to the same operation is accepted, so the refusal is caused by
// the encryption headers alone.
func TestEncryptionRequestsAreRefusedOnWrites(t *testing.T) {
	ops := []struct {
		name string
		send func(t *testing.T, s *server, h http.Header) reply
		key  string
	}{
		{"PutObject", func(t *testing.T, s *server, h http.Header) reply {
			return s.do(t, http.MethodPut, "/enc/put", []byte("data"), h)
		}, "put"},
		{"CopyObject", func(t *testing.T, s *server, h http.Header) reply {
			h.Set("X-Amz-Copy-Source", "/enc/src")
			return s.do(t, http.MethodPut, "/enc/copy", nil, h)
		}, "copy"},
		{"CreateMultipartUpload", func(t *testing.T, s *server, h http.Header) reply {
			return s.do(t, http.MethodPost, "/enc/mp?uploads", nil, h)
		}, "mp"},
		{"UploadPart", func(t *testing.T, s *server, h http.Header) reply {
			id := s.startUpload(t, "enc", "part")
			return s.do(t, http.MethodPut, "/enc/part?partNumber=1&uploadId="+id, []byte("data"), h)
		}, "part"},
		{"UploadPartCopy", func(t *testing.T, s *server, h http.Header) reply {
			id := s.startUpload(t, "enc", "pcopy")
			h.Set("X-Amz-Copy-Source", "/enc/src")
			return s.do(t, http.MethodPut, "/enc/pcopy?partNumber=1&uploadId="+id, nil, h)
		}, "pcopy"},
	}
	for _, op := range ops {
		for _, req := range encryptionRequests {
			t.Run(op.name+" "+req.name, func(t *testing.T) {
				s := newServer(t, &fakes.Authorizer{})
				s.createBucket(t, "enc")
				s.do(t, http.MethodPut, "/enc/src", []byte("source"), nil)

				r := op.send(t, s, cloneHeader(req.header))
				if r.status != http.StatusNotImplemented {
					t.Fatalf("status %d, want 501 NotImplemented (body %s)", r.status, r.body)
				}
				if code := errorCode(t, r.body); code != "NotImplemented" {
					t.Fatalf("code %q, want NotImplemented", code)
				}
				s.assertAbsent(t, "enc", op.key)
			})
		}
	}
}

// TestEncryptionRequestsAreRefusedOnReads checks that GET and HEAD refuse SSE-C
// key material and the other encryption headers with NotImplemented, instead of
// returning the object as if it were readable without the key.
func TestEncryptionRequestsAreRefusedOnReads(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, req := range encryptionRequests {
			t.Run(method+" "+req.name, func(t *testing.T) {
				s := newServer(t, &fakes.Authorizer{})
				s.createBucket(t, "enc")
				s.do(t, http.MethodPut, "/enc/obj", []byte("plain"), nil)

				r := s.do(t, method, "/enc/obj", nil, cloneHeader(req.header))
				if r.status != http.StatusNotImplemented {
					t.Fatalf("status %d, want 501 NotImplemented", r.status)
				}
			})
		}
	}
}

// TestPlainRequestsAreNotAffected is the control for the two tests above: the
// same operations without encryption headers succeed.
func TestPlainRequestsAreNotAffected(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "enc")
	if r := s.do(t, http.MethodPut, "/enc/plain", []byte("data"), nil); r.status != http.StatusOK {
		t.Fatalf("plain PutObject: status %d", r.status)
	}
	if r := s.do(t, http.MethodGet, "/enc/plain", nil, nil); r.status != http.StatusOK || string(r.body) != "data" {
		t.Fatalf("plain GetObject: status %d", r.status)
	}
}

// cloneHeader copies h, so that a table entry can be sent more than once.
func cloneHeader(h http.Header) http.Header {
	out := make(http.Header, len(h))
	for k, v := range h {
		out[k] = append([]string(nil), v...)
	}
	return out
}
