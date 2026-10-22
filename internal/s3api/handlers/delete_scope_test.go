package handlers_test

import (
	"encoding/xml"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// deleteResultDoc is the part of a DeleteResult document that the test reads.
type deleteResultDoc struct {
	Deleted []struct {
		Key string `xml:"Key"`
	} `xml:"Deleted"`
	Errors []struct {
		Key  string `xml:"Key"`
		Code string `xml:"Code"`
	} `xml:"Error"`
}

// TestDeleteObjectsAuthorizesEachKey uses a caller that may delete only keys
// under logs/. Such a caller must receive a result for every listed key: the
// keys it may delete are deleted, and the others are reported with AccessDenied
// inside an otherwise successful response. The whole request must not be
// denied, which is what a request-level check against an empty key would do.
func TestDeleteObjectsAuthorizesEachKey(t *testing.T) {
	scoped := &fakes.Authorizer{Deny: func(_ auth.Identity, action auth.Action, _, key string) bool {
		return action == auth.ActionDeleteObject && !strings.HasPrefix(key, "logs/")
	}}
	s := newServer(t, scoped)
	s.createBucket(t, "scoped")
	for _, key := range []string{"logs/a", "private/b"} {
		if r := s.do(t, http.MethodPut, "/scoped/"+key, []byte("data"), nil); r.status != http.StatusOK {
			t.Fatalf("seed %s: status %d", key, r.status)
		}
	}

	body := `<Delete><Object><Key>logs/a</Key></Object><Object><Key>private/b</Key></Object></Delete>`
	r := s.do(t, http.MethodPost, "/scoped?delete", []byte(body), nil)
	if r.status != http.StatusOK {
		t.Fatalf("DeleteObjects for a prefix-scoped caller: status %d, body %s", r.status, r.body)
	}
	var doc deleteResultDoc
	if err := xml.Unmarshal(r.body, &doc); err != nil {
		t.Fatalf("decode DeleteResult: %v", err)
	}
	if len(doc.Deleted) != 1 || doc.Deleted[0].Key != "logs/a" {
		t.Fatalf("deleted = %+v, want only logs/a", doc.Deleted)
	}
	if len(doc.Errors) != 1 || doc.Errors[0].Key != "private/b" || doc.Errors[0].Code != "AccessDenied" {
		t.Fatalf("errors = %+v, want private/b with AccessDenied", doc.Errors)
	}

	if r := s.do(t, http.MethodGet, "/scoped/private/b", nil, nil); r.status != http.StatusOK {
		t.Fatalf("private/b was deleted despite the denial: status %d", r.status)
	}
	s.assertAbsent(t, "scoped", "logs/a")
}
