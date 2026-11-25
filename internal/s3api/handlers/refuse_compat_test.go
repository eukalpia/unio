package handlers_test

import (
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestUnsupportedFeatureHeadersAreRefused checks that a write which asks for a
// feature this server does not implement is refused with NotImplemented and
// stores nothing, rather than being accepted without the feature.
func TestUnsupportedFeatureHeadersAreRefused(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "refuse")

	public := s.do(t, http.MethodPut, "/refuse/public", []byte("x"), http.Header{"X-Amz-Acl": {"public-read"}})
	if public.status != http.StatusNotImplemented {
		t.Fatalf("PUT with public-read: status %d, want 501", public.status)
	}
	lock := s.do(t, http.MethodPut, "/refuse/locked", []byte("x"), http.Header{"X-Amz-Object-Lock-Mode": {"COMPLIANCE"}})
	if lock.status != http.StatusNotImplemented {
		t.Fatalf("PUT with object lock: status %d, want 501", lock.status)
	}
	if got := s.do(t, http.MethodHead, "/refuse/public", nil, nil); got.status != http.StatusNotFound {
		t.Fatalf("refused PUT stored the object: HEAD status %d", got.status)
	}

	private := s.do(t, http.MethodPut, "/refuse/private", []byte("x"), http.Header{"X-Amz-Acl": {"private"}})
	if private.status != http.StatusOK {
		t.Fatalf("PUT with private: status %d, want 200", private.status)
	}
}

// TestCopyWithReplacedTagsIsRefused checks that a copy asking to replace the
// tags of the copy is refused, instead of keeping the source tags.
func TestCopyWithReplacedTagsIsRefused(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "copies")
	s.do(t, http.MethodPut, "/copies/src", []byte("x"), nil)

	r := s.do(t, http.MethodPut, "/copies/dst", nil, http.Header{
		"X-Amz-Copy-Source":       {"/copies/src"},
		"X-Amz-Tagging-Directive": {"REPLACE"},
	})
	if r.status != http.StatusNotImplemented {
		t.Fatalf("copy with REPLACE tagging: status %d, want 501", r.status)
	}
	if got := s.do(t, http.MethodHead, "/copies/dst", nil, nil); got.status != http.StatusNotFound {
		t.Fatalf("refused copy stored the destination: HEAD status %d", got.status)
	}
}
