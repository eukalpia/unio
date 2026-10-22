package objecttest

import (
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
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
