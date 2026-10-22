package fakes

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// testBucket is the bucket created by newTestLayer.
const testBucket = "test-bucket"

// testBody is an object.Body whose size, digest and checksum are set
// directly, so a test can declare any payload contract it needs.
type testBody struct {
	reader   io.Reader
	size     int64
	digest   [16]byte
	checksum object.Checksum
}

// Read implements io.Reader.
func (b *testBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

// Size implements object.Body.
func (b *testBody) Size() int64 { return b.size }

// MD5 implements object.Body.
func (b *testBody) MD5() [16]byte { return b.digest }

// Checksum implements object.Body.
func (b *testBody) Checksum() object.Checksum { return b.checksum }

// newTestBody returns a valid body that yields data.
func newTestBody(data []byte) *testBody {
	return &testBody{
		reader: bytes.NewReader(data),
		size:   int64(len(data)),
		digest: md5.Sum(data),
	}
}

// failingReader yields data and then returns err instead of io.EOF.
type failingReader struct {
	data []byte
	err  error
}

// Read implements io.Reader.
func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// newFailingBody returns a body whose reads fail with err after data.
func newFailingBody(data []byte, err error) *testBody {
	return &testBody{
		reader: &failingReader{data: data, err: err},
		size:   int64(len(data)),
	}
}

// newTestLayer returns a Layer with testBucket created.
func newTestLayer(t *testing.T) *Layer {
	t.Helper()
	l := NewLayer()
	if _, err := l.MakeBucket(context.Background(), testBucket, object.MakeBucketOptions{}); err != nil {
		t.Fatalf("MakeBucket(%q): %v", testBucket, err)
	}
	return l
}

// mustPut stores data under key in testBucket with default attributes.
func mustPut(t *testing.T, l *Layer, key string, data []byte) object.Info {
	t.Helper()
	info, err := l.PutObject(context.Background(), testBucket, key, newTestBody(data), object.PutOptions{})
	if err != nil {
		t.Fatalf("PutObject(%q): %v", key, err)
	}
	return info
}

// mustRead returns the full content of key in testBucket.
func mustRead(t *testing.T, l *Layer, key string) []byte {
	t.Helper()
	r, err := l.GetObject(context.Background(), testBucket, key, object.GetOptions{})
	if err != nil {
		t.Fatalf("GetObject(%q): %v", key, err)
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading %q: %v", key, err)
	}
	return data
}

// wantCode asserts that err is a *s3err.Error carrying code. An empty code
// means err must be nil.
func wantCode(t *testing.T, err error, code s3err.Code) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	var se *s3err.Error
	if !errors.As(err, &se) {
		t.Fatalf("error = %v (%T), want *s3err.Error with code %s", err, err, code)
	}
	if got := s3err.CodeOf(err); got != code {
		t.Fatalf("error code = %s (%v), want %s", got, err, code)
	}
}

// md5Hex returns the lower-case hex MD5 of data.
func md5Hex(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
