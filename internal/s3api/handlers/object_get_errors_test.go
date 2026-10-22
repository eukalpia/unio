package handlers_test

import (
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestGetObjectConditionalErrors checks the read preconditions of GetObject. A
// matching If-None-Match answers 304 with no body, and a failing If-Match
// answers 412 with an S3 error document.
func TestGetObjectConditionalErrors(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "cond")
	put := s.do(t, http.MethodPut, "/cond/k", []byte("0123456789"), nil)
	if put.status != http.StatusOK {
		t.Fatalf("put: status %d, body %s", put.status, put.body)
	}
	etag := put.header.Get("ETag")
	if etag == "" {
		t.Fatal("PUT returned no ETag")
	}

	cases := []struct {
		name   string
		header http.Header
		status int
		code   string // empty for a 304, which has no body
	}{
		{
			name:   "If-None-Match with the current ETag",
			header: hdr("If-None-Match", etag),
			status: http.StatusNotModified,
		},
		{
			name:   "If-Match with another ETag",
			header: hdr("If-Match", `"00000000000000000000000000000000"`),
			status: http.StatusPreconditionFailed,
			code:   "PreconditionFailed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := s.do(t, http.MethodGet, "/cond/k", nil, tc.header)
			if tc.code == "" {
				if r.status != tc.status || len(r.body) != 0 {
					t.Fatalf("status %d with %d body bytes, want %d and none", r.status, len(r.body), tc.status)
				}
				return
			}
			expectError(t, r, tc.status, tc.code)
		})
	}
}

// TestGetObjectRangeNotSatisfiable checks that a malformed or unsatisfiable
// Range header on a 10-byte object is refused with 416 InvalidRange.
func TestGetObjectRangeNotSatisfiable(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "ranges")
	if r := s.do(t, http.MethodPut, "/ranges/k", []byte("0123456789"), nil); r.status != http.StatusOK {
		t.Fatalf("put: status %d, body %s", r.status, r.body)
	}

	cases := []struct {
		name      string
		rangeSpec string
	}{
		{name: "first byte past the end", rangeSpec: "bytes=1000-2000"},
		{name: "suffix of zero bytes", rangeSpec: "bytes=-0"},
		{name: "last byte before first", rangeSpec: "bytes=5-2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := s.do(t, http.MethodGet, "/ranges/k", nil, hdr("Range", tc.rangeSpec))
			expectError(t, r, http.StatusRequestedRangeNotSatisfiable, "InvalidRange")
		})
	}
}
