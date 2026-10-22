package payload

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
)

func TestNewValidation(t *testing.T) {
	r := strings.NewReader("")
	cases := []struct {
		name    string
		r       io.Reader
		spec    Spec
		wantErr bool
	}{
		{name: "nil reader", r: nil, spec: Spec{}, wantErr: true},
		{name: "negative size", r: r, spec: Spec{Size: -1}, wantErr: true},
		{name: "zero size", r: r, spec: Spec{}},
		{name: "unknown algorithm", r: r, spec: Spec{Algorithm: object.ChecksumAlgorithm(6)}, wantErr: true},
		{name: "unknown algorithm with expected", r: r, spec: Spec{Algorithm: object.ChecksumAlgorithm(200), Expected: make([]byte, 4)}, wantErr: true},
		{name: "content-md5 empty", r: r, spec: Spec{ContentMD5: []byte{}}, wantErr: true},
		{name: "content-md5 15 bytes", r: r, spec: Spec{ContentMD5: make([]byte, 15)}, wantErr: true},
		{name: "content-md5 17 bytes", r: r, spec: Spec{ContentMD5: make([]byte, 17)}, wantErr: true},
		{name: "content-md5 16 bytes", r: r, spec: Spec{ContentMD5: make([]byte, 16)}},
		{name: "sha256 31 bytes", r: r, spec: Spec{SHA256: make([]byte, 31)}, wantErr: true},
		{name: "sha256 empty", r: r, spec: Spec{SHA256: []byte{}}, wantErr: true},
		{name: "sha256 32 bytes", r: r, spec: Spec{SHA256: make([]byte, 32)}},
		{name: "expected without algorithm", r: r, spec: Spec{Expected: make([]byte, 4)}, wantErr: true},
		{name: "empty expected without algorithm", r: r, spec: Spec{Expected: []byte{}}, wantErr: true},
		{name: "expected too short for CRC32", r: r, spec: Spec{Algorithm: object.ChecksumCRC32, Expected: make([]byte, 3)}, wantErr: true},
		{name: "expected too long for CRC32C", r: r, spec: Spec{Algorithm: object.ChecksumCRC32C, Expected: make([]byte, 8)}, wantErr: true},
		{name: "expected too long for SHA1", r: r, spec: Spec{Algorithm: object.ChecksumSHA1, Expected: make([]byte, 32)}, wantErr: true},
		{name: "algorithm without expected", r: r, spec: Spec{Algorithm: object.ChecksumCRC64NVME}},
		{name: "trailer without algorithm", r: r, spec: Spec{Trailer: trailerOf(object.Checksum{}, false)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := New(tc.r, tc.spec)
			if (err != nil) != tc.wantErr {
				t.Fatalf("New error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && b != nil {
				t.Fatal("New returned a Body together with an error")
			}
		})
	}
}

func TestNewAcceptsExactlyTheDigestLength(t *testing.T) {
	r := strings.NewReader("")
	for _, a := range algorithms() {
		want := len(checkDigest(a))
		if _, err := New(r, Spec{Algorithm: a, Expected: make([]byte, want)}); err != nil {
			t.Errorf("%s with %d-byte digest: %v", a, want, err)
		}
		if _, err := New(r, Spec{Algorithm: a, Expected: make([]byte, want+1)}); err == nil {
			t.Errorf("%s accepted a %d-byte digest", a, want+1)
		}
	}
}

func TestNewCopiesSpecSlices(t *testing.T) {
	data := checkInput
	md5sum := checkMD5(data)
	expected := checkDigest(object.ChecksumCRC32C)
	spec := Spec{
		Size:       int64(len(data)),
		ContentMD5: md5sum,
		Algorithm:  object.ChecksumCRC32C,
		Expected:   expected,
	}
	b, err := New(newScript(step{data: data, err: io.EOF}), spec)
	if err != nil {
		t.Fatal(err)
	}
	// Overwrite the caller's slices after New: the Body must keep its own copies.
	for i := range md5sum {
		md5sum[i] = 0
	}
	copy(expected, bytes.Repeat([]byte{0xff}, len(expected)))
	if _, err := drain(t, b, 4); err != nil {
		t.Fatalf("Body observed the caller's mutation: %v", err)
	}
}
