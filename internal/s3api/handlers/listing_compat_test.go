package handlers_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestListObjectsV2EchoesEmptyContinuationToken checks that a request that
// carries an empty continuation-token gets the element back, empty, as S3
// sends it.
func TestListObjectsV2EchoesEmptyContinuationToken(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "listv2")
	s.do(t, http.MethodPut, "/listv2/a", []byte("1"), nil)

	r := s.do(t, http.MethodGet, "/listv2?list-type=2&continuation-token=", nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("status %d: %s", r.status, r.body)
	}
	if !strings.Contains(string(r.body), "<ContinuationToken></ContinuationToken>") {
		t.Fatalf("response has no empty ContinuationToken element:\n%s", r.body)
	}

	plain := s.do(t, http.MethodGet, "/listv2?list-type=2", nil, nil)
	if strings.Contains(string(plain.body), "ContinuationToken") {
		t.Fatalf("response without the parameter carries a ContinuationToken element:\n%s", plain.body)
	}
}
