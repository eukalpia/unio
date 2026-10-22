package handlers_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
	"github.com/eukalpia/unio/internal/s3err"
)

// notModifiedLayer reports NotModified the way the object engine does: with
// the object description in the cause, which the front end sends as the
// validators of the 304 response.
type notModifiedLayer struct {
	object.Layer
}

// notModifiedCause carries the description of the object in a NotModified error.
type notModifiedCause struct{ info object.Info }

func (notModifiedCause) Error() string { return "not modified" }

func (c notModifiedCause) NotModifiedInfo() object.Info { return c.info }

func (l notModifiedLayer) GetObject(ctx context.Context, bucket, key string, opts object.GetOptions) (*object.Reader, error) {
	r, err := l.Layer.GetObject(ctx, bucket, key, opts)
	if s3err.Is(err, s3err.NotModified) {
		info, herr := l.Layer.HeadObject(ctx, bucket, key, object.GetOptions{})
		if herr != nil {
			return nil, herr
		}
		return nil, s3err.Wrap(s3err.NotModified, notModifiedCause{info: info})
	}
	return r, err
}

func (l notModifiedLayer) HeadObject(ctx context.Context, bucket, key string, opts object.GetOptions) (object.Info, error) {
	info, err := l.Layer.HeadObject(ctx, bucket, key, opts)
	if s3err.Is(err, s3err.NotModified) {
		current, herr := l.Layer.HeadObject(ctx, bucket, key, object.GetOptions{})
		if herr != nil {
			return object.Info{}, herr
		}
		return object.Info{}, s3err.Wrap(s3err.NotModified, notModifiedCause{info: current})
	}
	return info, err
}

// TestNotModifiedResponseCarriesValidators checks that a 304 answer to a
// conditional GET or HEAD names the object's ETag and Last-Modified, as RFC
// 9110 requires.
func TestNotModifiedResponseCarriesValidators(t *testing.T) {
	layer := notModifiedLayer{fakes.NewLayer()}
	s := newServerWithLayer(t, layer, &fakes.Authorizer{})
	s.createBucket(t, "cond")
	put := s.do(t, http.MethodPut, "/cond/k", []byte("data"), nil)
	etag := put.header.Get("ETag")

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r := s.do(t, method, "/cond/k", nil, http.Header{"If-None-Match": {etag}})
		if r.status != http.StatusNotModified {
			t.Fatalf("%s: status %d, want 304", method, r.status)
		}
		if got := r.header.Get("ETag"); got != etag {
			t.Fatalf("%s: ETag = %q, want %q", method, got, etag)
		}
		if r.header.Get("Last-Modified") == "" {
			t.Fatalf("%s: 304 has no Last-Modified", method)
		}
	}
}
