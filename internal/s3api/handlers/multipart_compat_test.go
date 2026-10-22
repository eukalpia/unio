package handlers_test

import (
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestCreateMultipartUploadReportsChecksumAlgorithm checks that the algorithm
// of an upload is sent in the x-amz-checksum-algorithm header of the response.
func TestCreateMultipartUploadReportsChecksumAlgorithm(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "uploads")

	withSum := s.do(t, http.MethodPost, "/uploads/k?uploads", nil,
		http.Header{"X-Amz-Checksum-Algorithm": {"SHA256"}})
	if got := withSum.header.Get("X-Amz-Checksum-Algorithm"); got != "SHA256" {
		t.Fatalf("x-amz-checksum-algorithm = %q, want SHA256", got)
	}

	plain := s.do(t, http.MethodPost, "/uploads/k2?uploads", nil, nil)
	if got := plain.header.Get("X-Amz-Checksum-Algorithm"); got != "" {
		t.Fatalf("x-amz-checksum-algorithm = %q on an upload without a checksum, want none", got)
	}
}

// TestCompleteMultipartUploadRefusesUnverifiedChecksum checks that a
// completion carrying a digest of the whole object is refused, since the layer
// cannot verify it, and that the upload is left in place.
func TestCompleteMultipartUploadRefusesUnverifiedChecksum(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "complete")
	id := s.startUpload(t, "complete", "k")

	r := s.do(t, http.MethodPost, "/complete/k?uploadId="+id, nil,
		http.Header{"X-Amz-Checksum-Sha256": {"Ok6Cs5b96ux6+MWQkJO7UBT5sKPBeXBLwvj/hK89smg=-1"}})
	if r.status != http.StatusNotImplemented {
		t.Fatalf("complete with a checksum: status %d, want 501", r.status)
	}
	if got := s.do(t, http.MethodHead, "/complete/k", nil, nil); got.status != http.StatusNotFound {
		t.Fatalf("refused completion stored the object: HEAD status %d", got.status)
	}
}
