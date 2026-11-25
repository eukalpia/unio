package router

import (
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

func TestBucketPolicyRoutes(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for method, want := range map[string]Op{"GET": OpGetBucketPolicy, "PUT": OpPutBucketPolicy, "DELETE": OpDeleteBucketPolicy} {
		rt, err := r.Match(Request{Method: method, Path: "/photos", Query: "policy="})
		if err != nil || rt.Op != want || rt.Bucket != "photos" {
			t.Errorf("%s ?policy: op %s bucket %q err %v, want %s", method, rt.Op, rt.Bucket, err, want)
		}
	}
	if _, err := r.Match(Request{Method: "POST", Path: "/photos", Query: "policy="}); s3err.CodeOf(err) != s3err.MethodNotAllowed {
		t.Errorf("POST ?policy: %v, want MethodNotAllowed", err)
	}
}

func TestObjectPolicyIsRefused(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Match(Request{Method: "PUT", Path: "/photos/a.jpg", Query: "policy="}); s3err.CodeOf(err) != s3err.NotImplemented {
		t.Errorf("object ?policy: %v, want NotImplemented", err)
	}
}
