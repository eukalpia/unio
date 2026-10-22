package handlers_test

import (
	"context"
	"encoding/xml"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
	"github.com/eukalpia/unio/internal/s3err"
)

// partNumberLayer answers every part beyond the first the way the object
// engine answers a part number that names no part of a single-part object.
type partNumberLayer struct {
	object.Layer
}

func (l partNumberLayer) GetObject(ctx context.Context, bucket, key string, opts object.GetOptions) (*object.Reader, error) {
	if opts.PartNumber > 1 {
		return nil, noSuchPartError()
	}
	return l.Layer.GetObject(ctx, bucket, key, opts)
}

func (l partNumberLayer) HeadObject(ctx context.Context, bucket, key string, opts object.GetOptions) (object.Info, error) {
	if opts.PartNumber > 1 {
		return object.Info{}, noSuchPartError()
	}
	return l.Layer.HeadObject(ctx, bucket, key, opts)
}

// TestPartNumberBeyondTheObjectIsRangeNotSatisfiable checks that a partNumber
// that names no part answers 416 InvalidPartNumber, and that a malformed one is
// still InvalidArgument.
func TestPartNumberBeyondTheObjectIsRangeNotSatisfiable(t *testing.T) {
	s := newServerWithLayer(t, partNumberLayer{fakes.NewLayer()}, &fakes.Authorizer{})
	s.createBucket(t, "parts")
	s.do(t, http.MethodPut, "/parts/k", []byte("data"), nil)

	beyond := s.do(t, http.MethodGet, "/parts/k?partNumber=2", nil, nil)
	var e s3Error
	if err := xml.Unmarshal(beyond.body, &e); err != nil {
		t.Fatal(err)
	}
	if beyond.status != http.StatusRequestedRangeNotSatisfiable || e.Code != string(s3err.InvalidPartNumber) {
		t.Fatalf("partNumber=2: status %d code %q, want 416 InvalidPartNumber", beyond.status, e.Code)
	}

	// Far beyond any object: still the layer's answer, not a malformed request.
	far := s.do(t, http.MethodHead, "/parts/k?partNumber=10001", nil, nil)
	if far.status != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("partNumber=10001 on HEAD: status %d, want 416", far.status)
	}

	zero := s.do(t, http.MethodGet, "/parts/k?partNumber=0", nil, nil)
	if err := xml.Unmarshal(zero.body, &e); err != nil {
		t.Fatal(err)
	}
	if zero.status != http.StatusBadRequest || e.Code != "InvalidArgument" {
		t.Fatalf("partNumber=0: status %d code %q, want 400 InvalidArgument", zero.status, e.Code)
	}
}

// noSuchPart is the cause the object layer attaches to InvalidArgument for a
// part number that names no part of the object.
type noSuchPart struct{}

func (noSuchPart) Error() string { return "the object has no part with that number" }

func (noSuchPart) NoSuchPart() bool { return true }

// noSuchPartError is the layer's error for a part number that names no part.
func noSuchPartError() error {
	return s3err.Wrap(s3err.InvalidArgument, noSuchPart{})
}
