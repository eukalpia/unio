package handlers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// tagging wraps the tags in a Tagging document with the S3 namespace.
func tagging(tags string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Tagging xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><TagSet>` + tags + `</TagSet></Tagging>`
}

// tagElements returns n Tag elements with distinct keys.
func tagElements(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "<Tag><Key>k%d</Key><Value>v</Value></Tag>", i)
	}
	return b.String()
}

// TestObjectTaggingBodies checks that a Tagging body is decoded strictly and
// bounded. Every hostile or malformed body is refused with its S3 code, and the
// refused request leaves the tags of the object as they were.
func TestObjectTaggingBodies(t *testing.T) {
	const (
		malformed = "MalformedXML"
		invalid   = "InvalidTag"
	)
	cases := []struct {
		name string
		body string
		code string
	}{
		{"empty body", "", malformed},
		{"not xml", "tags", malformed},
		{"wrong root", `<Delete><TagSet/></Delete>`, malformed},
		{"no TagSet", `<Tagging></Tagging>`, malformed},
		{"two TagSet elements", `<Tagging><TagSet/><TagSet/></Tagging>`, malformed},
		{"missing Value", tagging(`<Tag><Key>a</Key></Tag>`), malformed},
		{"missing Key", tagging(`<Tag><Value>a</Value></Tag>`), malformed},
		{"repeated Key", tagging(`<Tag><Key>a</Key><Key>b</Key><Value>c</Value></Tag>`), malformed},
		{"unknown element", tagging(`<Tag><Key>a</Key><Value>b</Value><Extra/></Tag>`), malformed},
		{"element outside TagSet", `<Tagging><TagSet/><Extra/></Tagging>`, malformed},
		{"markup in value", tagging(`<Tag><Key>a</Key><Value><b>x</b></Value></Tag>`), malformed},
		{"doctype", `<!DOCTYPE t [<!ENTITY e "x">]>` + tagging(`<Tag><Key>a</Key><Value>&e;</Value></Tag>`), malformed},
		{"entity expansion", `<!DOCTYPE t [<!ENTITY a "aaaaaaaaaa"><!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">]>` +
			tagging(`<Tag><Key>a</Key><Value>&b;</Value></Tag>`), malformed},
		{"deep nesting", tagging(strings.Repeat("<Tag><Key>", 300)), malformed},
		{"trailing content", tagging("") + "<x/>", malformed},
		{"eleven tags", tagging(tagElements(object.MaxTags + 1)), invalid},
		{"duplicate keys", tagging(`<Tag><Key>a</Key><Value>1</Value></Tag><Tag><Key>a</Key><Value>2</Value></Tag>`), invalid},
		{"reserved prefix", tagging(`<Tag><Key>aws:x</Key><Value>1</Value></Tag>`), invalid},
		{"key of 129 characters", tagging(`<Tag><Key>` + strings.Repeat("k", object.MaxTagKeyLength+1) + `</Key><Value>1</Value></Tag>`), invalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t, &fakes.Authorizer{})
			s.createBucket(t, "tags")
			if r := s.do(t, http.MethodPut, "/tags/k", []byte("x"), nil); r.status != http.StatusOK {
				t.Fatalf("put object: status %d", r.status)
			}
			if r := s.do(t, http.MethodPut, "/tags/k?tagging", []byte(tagging(`<Tag><Key>keep</Key><Value>me</Value></Tag>`)), nil); r.status != http.StatusOK {
				t.Fatalf("put tags: status %d, body %s", r.status, r.body)
			}

			expectError(t, s.do(t, http.MethodPut, "/tags/k?tagging", []byte(tc.body), nil), http.StatusBadRequest, tc.code)

			got := s.do(t, http.MethodGet, "/tags/k?tagging", nil, nil)
			if got.status != http.StatusOK || !strings.Contains(string(got.body), "<Key>keep</Key><Value>me</Value>") {
				t.Fatalf("refused request changed the tags: status %d, body %s", got.status, got.body)
			}
		})
	}
}

// TestObjectTaggingBodyLimit checks that a Tagging body over the XML limit is
// refused before it is read.
func TestObjectTaggingBodyLimit(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "tags")
	s.do(t, http.MethodPut, "/tags/k", []byte("x"), nil)
	big := []byte(strings.Repeat(" ", object.MaxXMLBodySize+1))
	expectError(t, s.do(t, http.MethodPut, "/tags/k?tagging", big, nil), http.StatusBadRequest, "MaxMessageLengthExceeded")
}

// TestObjectTaggingEmptySetClears checks that an empty TagSet removes every tag.
func TestObjectTaggingEmptySetClears(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "tags")
	s.do(t, http.MethodPut, "/tags/k", []byte("x"), nil)
	if r := s.do(t, http.MethodPut, "/tags/k?tagging", []byte(tagging(`<Tag><Key>a</Key><Value>b</Value></Tag>`)), nil); r.status != http.StatusOK {
		t.Fatalf("put tags: status %d", r.status)
	}
	if r := s.do(t, http.MethodPut, "/tags/k?tagging", []byte(tagging("")), nil); r.status != http.StatusOK {
		t.Fatalf("clear tags: status %d, body %s", r.status, r.body)
	}
	got := s.do(t, http.MethodGet, "/tags/k?tagging", nil, nil)
	if got.status != http.StatusOK || strings.Contains(string(got.body), "<Tag>") {
		t.Fatalf("tags after clear: status %d, body %s", got.status, got.body)
	}
}
