package engine_test

import (
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// notModifiedInfo is the interface a front end uses to read the validators of
// a NotModified error.
type notModifiedInfo interface {
	NotModifiedInfo() object.Info
}

// TestNotModifiedCarriesTheValidators checks that a read whose conditions hold
// reports NotModified with the object description in its cause, so that the
// front end can send the ETag and Last-Modified of a 304 response.
func TestNotModifiedCarriesTheValidators(t *testing.T) {
	h := newHarness(t, engine.Options{})
	ctx := t.Context()
	if _, err := h.MakeBucket(ctx, testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	put, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody([]byte("data")), object.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	conds := object.Conditions{IfNoneMatch: put.ETag}

	_, err = h.GetObject(ctx, testBucket, "k", object.GetOptions{Conditions: conds})
	if !s3err.Is(err, s3err.NotModified) {
		t.Fatalf("get: %v, want NotModified", err)
	}
	var nm notModifiedInfo
	if !errors.As(err, &nm) {
		t.Fatalf("get: NotModified error has no validators: %v", err)
	}
	if got := nm.NotModifiedInfo(); got.ETag != put.ETag || got.ModTime.IsZero() {
		t.Fatalf("validators = etag %q modified %v, want etag %q", got.ETag, got.ModTime, put.ETag)
	}

	_, err = h.HeadObject(ctx, testBucket, "k", object.GetOptions{Conditions: conds})
	if !s3err.Is(err, s3err.NotModified) || !errors.As(err, &nm) {
		t.Fatalf("head: %v, want NotModified with validators", err)
	}

	// A failed precondition is not a 304 and carries no validators.
	_, err = h.GetObject(ctx, testBucket, "k", object.GetOptions{Conditions: object.Conditions{IfMatch: "other"}})
	if !s3err.Is(err, s3err.PreconditionFailed) || errors.As(err, &nm) {
		t.Fatalf("failed If-Match: %v, want a plain PreconditionFailed", err)
	}
}
