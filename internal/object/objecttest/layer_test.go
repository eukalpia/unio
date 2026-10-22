package objecttest

import (
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestFakeLayerConformance(t *testing.T) {
	RunLayerSuite(t, func(t *testing.T) object.Layer { return NewLayer() })
}

func TestBodyContract(t *testing.T) {
	data := []byte("0123456789")
	boom := errors.New("boom")
	tests := []struct {
		name    string
		body    *Body
		want    []byte
		wantErr error
	}{
		{"clean", NewBody(data), data, nil},
		{"empty", NewBody(nil), nil, nil},
		{"error before end", NewBody(data, FailAfter(4, boom)), data[:4], boom},
		{"error at end with bytes", NewBody(data, FinalError(boom)), data, boom},
		{"declared longer", NewBody(data, Declared(12)), data, io.ErrUnexpectedEOF},
		{"declared shorter", NewBody(data, Declared(6)), data[:6], nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := io.ReadAll(tt.body)
			if string(got) != string(tt.want) {
				t.Fatalf("delivered %q, want %q", got, tt.want)
			}
			if !errors.Is(err, tt.wantErr) && !(err == nil && tt.wantErr == nil) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBodyDigestsAfterEOF(t *testing.T) {
	data := []byte("123456789")
	b := NewBody(data, WithChecksum(object.ChecksumCRC32))
	if _, err := io.ReadAll(b); err != nil {
		t.Fatal(err)
	}
	if b.MD5() != md5Of(data) {
		t.Fatal("MD5 mismatch")
	}
	if got := b.Checksum(); got.Algorithm != object.ChecksumCRC32 || len(got.Value) != 4 {
		t.Fatalf("checksum = %+v", got)
	}
}

// overlongBody declares size bytes but yields size+1 of them.
type overlongBody struct {
	data []byte
	pos  int
}

func (b *overlongBody) Read(p []byte) (int, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.pos:])
	b.pos += n
	if b.pos >= len(b.data) {
		return n, io.EOF
	}
	return n, nil
}

func (b *overlongBody) Size() int64               { return int64(len(b.data) - 1) }
func (b *overlongBody) MD5() [16]byte             { return [16]byte{} }
func (b *overlongBody) Checksum() object.Checksum { return object.Checksum{} }

// TestFakeRejectsOverlongBody checks that a body yielding more bytes than its
// declared size is an InvalidRequest, as the object.Body contract requires.
func TestFakeRejectsOverlongBody(t *testing.T) {
	l := NewLayer()
	if _, err := l.MakeBucket(t.Context(), testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	_, err := l.PutObject(t.Context(), testBucket, "k", &overlongBody{data: []byte("123456")}, object.PutOptions{})
	if !s3err.Is(err, s3err.InvalidRequest) {
		t.Fatalf("err = %v, want InvalidRequest", err)
	}
}
