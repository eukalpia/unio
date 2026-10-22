package handlers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// deleteBody builds a DeleteObjects document of n keys. The first key is
// "keep", so a test can check that a rejected request deleted nothing.
func deleteBody(n int) []byte {
	var b strings.Builder
	b.WriteString("<Delete><Object><Key>keep</Key></Object>")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "<Object><Key>k%d</Key></Object>", i)
	}
	b.WriteString("</Delete>")
	return []byte(b.String())
}

// TestDeleteObjectsKeyLimit checks the key count of DeleteObjects. A document
// naming more than 1000 keys is refused with MalformedXML, and nothing in it
// is deleted. A document of exactly 1000 keys is accepted, which shows that the
// refusal is about the count and not about the size of the document.
func TestDeleteObjectsKeyLimit(t *testing.T) {
	cases := []struct {
		name   string
		keys   int
		status int
		code   string // empty when the request must succeed
	}{
		{name: "1001 keys", keys: 1001, status: http.StatusBadRequest, code: "MalformedXML"},
		{name: "1000 keys", keys: 1000, status: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t, &fakes.Authorizer{})
			s.createBucket(t, "batch")
			if r := s.do(t, http.MethodPut, "/batch/keep", []byte("kept"), nil); r.status != http.StatusOK {
				t.Fatalf("put: status %d, body %s", r.status, r.body)
			}

			r := s.do(t, http.MethodPost, "/batch?delete", deleteBody(tc.keys), nil)
			if tc.code == "" {
				if r.status != tc.status || strings.Contains(string(r.body), "<Error>") {
					t.Fatalf("status %d, want %d with no per-key errors; body %s", r.status, tc.status, r.body)
				}
				return
			}
			expectError(t, r, tc.status, tc.code)

			get := s.do(t, http.MethodGet, "/batch/keep", nil, nil)
			if get.status != http.StatusOK {
				t.Fatalf("rejected DeleteObjects removed the first key: GET status %d", get.status)
			}
		})
	}
}
