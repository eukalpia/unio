package router

import (
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

// FuzzMatch checks that routing never panics and that a successful match
// always names an operation, and a bucket for every non-service operation.
func FuzzMatch(f *testing.F) {
	seeds := []struct{ method, host, path, query string }{
		{"GET", "", "/", ""},
		{"PUT", "", "/photos/a%2Fb", ""},
		{"POST", "photos.s3.unio.local", "/k", "uploads="},
		{"PUT", "photos.s3.unio.local:9000", "/k", "partNumber=1&uploadId=x"},
		{"GET", "", "/photos", "list-type=2&prefix=%zz"},
		{"DELETE", "", "/%00", ""},
	}
	for _, s := range seeds {
		f.Add(s.method, s.host, s.path, s.query, false)
	}
	r, err := New([]string{"s3.unio.local"})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, method, host, path, query string, copySource bool) {
		route, err := r.Match(Request{Method: method, Host: host, Path: path, Query: query, CopySource: copySource})
		if err != nil {
			if s3err.CodeOf(err) == s3err.InternalError {
				t.Fatalf("client input produced an internal error: %v", err)
			}
			return
		}
		if route.Op == OpUnknown {
			t.Fatal("successful match without an operation")
		}
		if route.Key != "" && route.Bucket == "" {
			t.Fatalf("key %q without bucket", route.Key)
		}
	})
}
