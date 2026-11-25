package handlers_test

import (
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestCreateBucketAcceptsRecreateInDefaultRegion checks that a request for a
// bucket the caller already owns succeeds in us-east-1, as S3 does there.
func TestCreateBucketAcceptsRecreateInDefaultRegion(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "recreate-me")
	r := s.do(t, http.MethodPut, "/recreate-me", nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("second create: status %d, body %s; want 200", r.status, r.body)
	}
}
