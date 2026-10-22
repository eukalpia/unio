package handlers_test

import (
	"encoding/xml"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// bucketList is the part of a ListAllMyBucketsResult document the tests read.
type bucketList struct {
	Buckets []struct {
		Name string `xml:"Name"`
	} `xml:"Buckets>Bucket"`
	ContinuationToken string `xml:"ContinuationToken"`
	Prefix            string `xml:"Prefix"`
}

// listBuckets sends a ListBuckets request with the raw query q and decodes it.
func listBuckets(t *testing.T, s *server, q string) bucketList {
	t.Helper()
	r := s.do(t, http.MethodGet, "/?"+q, nil, nil)
	if r.status != http.StatusOK {
		t.Fatalf("list buckets %q: status %d, body %s", q, r.status, r.body)
	}
	var out bucketList
	if err := xml.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("list buckets %q: %v\n%s", q, err, r.body)
	}
	return out
}

// TestListBucketsPages checks the page size, the continuation token and the
// prefix of ListBuckets.
func TestListBucketsPages(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	for _, name := range []string{"alpha-1", "beta-1", "beta-2"} {
		s.createBucket(t, name)
	}

	page := listBuckets(t, s, "max-buckets=2")
	if len(page.Buckets) != 2 || page.Buckets[0].Name != "alpha-1" || page.Buckets[1].Name != "beta-1" {
		t.Fatalf("first page = %+v, want alpha-1 and beta-1", page.Buckets)
	}
	if page.ContinuationToken == "" {
		t.Fatal("first page has no continuation token, but more buckets follow")
	}

	next := listBuckets(t, s, "max-buckets=2&continuation-token="+page.ContinuationToken)
	if len(next.Buckets) != 1 || next.Buckets[0].Name != "beta-2" || next.ContinuationToken != "" {
		t.Fatalf("second page = %+v token %q, want beta-2 and no token", next.Buckets, next.ContinuationToken)
	}

	filtered := listBuckets(t, s, "prefix=beta")
	if len(filtered.Buckets) != 2 || filtered.Prefix != "beta" {
		t.Fatalf("prefix listing = %+v prefix %q, want two beta buckets", filtered.Buckets, filtered.Prefix)
	}

	last := listBuckets(t, s, "max-buckets=3")
	if len(last.Buckets) != 3 || last.ContinuationToken != "" {
		t.Fatalf("listing that fits = %+v token %q, want three buckets and no token", last.Buckets, last.ContinuationToken)
	}
}

// TestListBucketsRejectsBadPaging checks the argument errors of the paging
// parameters.
func TestListBucketsRejectsBadPaging(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "only-one")
	tests := []struct {
		name  string
		query string
	}{
		{"zero page", "max-buckets=0"},
		{"page over the limit", "max-buckets=10001"},
		{"token that is not base64", "continuation-token=%25%25"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := s.do(t, http.MethodGet, "/?"+tt.query, nil, nil)
			var e s3Error
			if err := xml.Unmarshal(r.body, &e); err != nil {
				t.Fatal(err)
			}
			if r.status != http.StatusBadRequest || e.Code != "InvalidArgument" {
				t.Fatalf("status %d code %q, want 400 InvalidArgument", r.status, e.Code)
			}
		})
	}
}
