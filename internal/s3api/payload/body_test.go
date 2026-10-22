package payload

import (
	"crypto/md5"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
)

func TestReadCapsBufferToDeclaredRemainder(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    []int // buffer lengths offered to the underlying reader
		code    s3err.Code
	}{
		{name: "excess byte is detected with one extra byte offered", payload: checkInput + "0", want: []int{10}, code: s3err.InvalidRequest},
		{name: "exact payload then one byte offered at EOF", payload: checkInput, want: []int{10, 1}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := &recordingReader{r: strings.NewReader(tc.payload)}
			b, err := New(src, Spec{Size: int64(len(checkInput))})
			if err != nil {
				t.Fatal(err)
			}
			_, err = drain(t, b, 64)
			requireCode(t, err, tc.code)
			if !slices.Equal(src.caps, tc.want) {
				t.Fatalf("buffer lengths %v, want %v", src.caps, tc.want)
			}
		})
	}
}

func TestReadWithEmptyBufferDoesNotVerify(t *testing.T) {
	b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{
		Size:       int64(len(checkInput)),
		ContentMD5: make([]byte, md5.Size),
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := b.Read(nil)
	if n != 0 || err != nil {
		t.Fatalf("Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
	_, err = drain(t, b, 4)
	requireCode(t, err, s3err.BadDigest)
}

func TestReadPassesThroughZeroCountReads(t *testing.T) {
	b, err := New(newScript(step{}, step{data: checkInput, err: io.EOF}), Spec{Size: int64(len(checkInput))})
	if err != nil {
		t.Fatal(err)
	}
	n, err := b.Read(make([]byte, 16))
	if n != 0 || err != nil {
		t.Fatalf("Read = (%d, %v), want (0, nil) from a zero-count underlying read", n, err)
	}
	got, err := drain(t, b, 16)
	requireCode(t, err, "")
	if string(got) != checkInput {
		t.Fatalf("delivered %q", got)
	}
}

func TestReadAfterEOFKeepsReturningEOF(t *testing.T) {
	b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{Size: int64(len(checkInput))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain(t, b, 16); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if n, err := b.Read(make([]byte, 8)); n != 0 || err != io.EOF {
			t.Fatalf("Read after EOF = (%d, %v), want (0, EOF)", n, err)
		}
	}
}

func TestAccessors(t *testing.T) {
	b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{
		Size:      int64(len(checkInput)),
		Algorithm: object.ChecksumCRC32C,
		Expected:  be32(checkCRC32C),
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.Size() != int64(len(checkInput)) {
		t.Fatalf("Size = %d", b.Size())
	}
	if n, err := b.Read(make([]byte, 4)); n != 4 || err != nil {
		t.Fatalf("first read = (%d, %v)", n, err)
	}
	// Before io.EOF the digests cover the bytes accepted so far.
	if got, want := b.MD5(), md5.Sum([]byte(checkInput[:4])); got != want {
		t.Fatalf("partial MD5 = %x, want %x", got, want)
	}
	if _, err := drain(t, b, 16); err != nil {
		t.Fatal(err)
	}
	if got, want := b.MD5(), md5.Sum([]byte(checkInput)); got != want {
		t.Fatalf("MD5 = %x, want %x", got, want)
	}
	want := object.Checksum{Algorithm: object.ChecksumCRC32C, Type: object.ChecksumFullObject, Value: be32(checkCRC32C)}
	got := b.Checksum()
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Checksum mismatch (-want +got):\n%s", diff)
	}
	// The returned digest must be a copy: mutating it cannot affect later calls.
	got.Value[0] ^= 0xff
	if again := b.Checksum(); again.Value[0] != want.Value[0] {
		t.Fatal("Checksum returned a slice that aliases internal state")
	}
}

func TestChecksumWithoutAlgorithmIsZero(t *testing.T) {
	b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{Size: int64(len(checkInput))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := drain(t, b, 16); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(object.Checksum{}, b.Checksum()); diff != "" {
		t.Fatalf("Checksum without algorithm (-want +got):\n%s", diff)
	}
}

func TestStickyFailureAfterReaderError(t *testing.T) {
	b, err := New(newScript(step{data: "1234", err: io.ErrClosedPipe}, step{data: checkInput, err: io.EOF}), Spec{Size: int64(len(checkInput))})
	if err != nil {
		t.Fatal(err)
	}
	_, err = drain(t, b, 2)
	requireCode(t, err, s3err.IncompleteBody)
	// Later calls keep failing even when the reader would now succeed.
	if n, err2 := b.Read(make([]byte, 8)); n != 0 || err2 != err {
		t.Fatalf("Read after failure = (%d, %v), want (0, %v)", n, err2, err)
	}
}

func TestReadPathDoesNotAllocate(t *testing.T) {
	const runs = 64
	spec := Spec{
		Size:      int64(benchReadSize) * (runs + 2),
		Algorithm: object.ChecksumCRC32C,
	}
	b, err := New(zeroReader{}, spec)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, benchReadSize)
	allocs := testing.AllocsPerRun(runs, func() {
		if _, err := b.Read(buf); err != nil {
			t.Errorf("Read: %v", err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Read allocates %.1f times per call, want 0", allocs)
	}
}
