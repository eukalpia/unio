package payload

import (
	"bytes"
	"crypto/md5"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestNeverEOFBeforeVerification checks that a failing final check is reported
// instead of io.EOF, for every way the last bytes can arrive.
func TestNeverEOFBeforeVerification(t *testing.T) {
	tests := []struct {
		name  string
		steps []step
	}{
		{name: "last bytes with eof", steps: []step{{data: checkInput, err: io.EOF}}},
		{name: "last bytes then separate eof", steps: []step{{data: checkInput}, {err: io.EOF}}},
		{name: "last bytes split", steps: []step{{data: checkInput[:5]}, {data: checkInput[5:], err: io.EOF}}},
	}
	for _, tc := range tests {
		for _, bufSize := range []int{1, 4, 64} {
			t.Run(tc.name, func(t *testing.T) {
				b, err := New(newScript(tc.steps...), Spec{
					Size:       int64(len(checkInput)),
					ContentMD5: make([]byte, md5.Size),
				})
				if err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, bufSize)
				var delivered []byte
				for range maxReads {
					n, err := b.Read(buf)
					delivered = append(delivered, buf[:n]...)
					if err == io.EOF {
						t.Fatal("io.EOF returned although the Content-MD5 does not match")
					}
					if err != nil {
						requireCode(t, err, s3err.BadDigest)
						if !bytes.HasPrefix([]byte(checkInput), delivered) {
							t.Fatalf("delivered %q, which is not a prefix of the payload", delivered)
						}
						return
					}
				}
				t.Fatal("no terminal error")
			})
		}
	}
}

// TestVerificationOrder checks that the first failing check decides the error.
func TestVerificationOrder(t *testing.T) {
	badMD5 := make([]byte, md5.Size)
	badSHA := make([]byte, 32)
	badCRC := corrupt(be32(checkCRC32C))
	trailerOK := trailerOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Value: be32(checkCRC32C)}, true)
	trailerBad := trailerOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Value: badCRC}, true)

	tests := []struct {
		name string
		spec Spec
		code s3err.Code
	}{
		{name: "all wrong", spec: Spec{ContentMD5: badMD5, SHA256: badSHA, Algorithm: object.ChecksumCRC32C, Expected: badCRC, Trailer: trailerBad}, code: s3err.BadDigest},
		{name: "sha256 before expected", spec: Spec{SHA256: badSHA, Algorithm: object.ChecksumCRC32C, Expected: badCRC}, code: s3err.XAmzContentSHA256Mismatch},
		{name: "expected before trailer", spec: Spec{Algorithm: object.ChecksumCRC32C, Expected: badCRC, Trailer: trailerBad}, code: s3err.BadDigest},
		{name: "trailer last", spec: Spec{Algorithm: object.ChecksumCRC32C, Trailer: trailerBad}, code: s3err.XAmzContentChecksumMismatch},
		{name: "trailer ok after earlier checks pass", spec: Spec{Algorithm: object.ChecksumCRC32C, Expected: be32(checkCRC32C), Trailer: trailerOK}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Size = int64(len(checkInput))
			b, err := New(newScript(step{data: checkInput, err: io.EOF}), tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			_, err = drain(t, b, 64)
			requireCode(t, err, tc.code)
		})
	}
}

// TestTrailerCalledOnce checks that the trailer is consulted at most once, and
// only when the earlier checks passed.
func TestTrailerCalledOnce(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		calls := 0
		b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{
			Size:      int64(len(checkInput)),
			Algorithm: object.ChecksumCRC32C,
			Trailer: func() (object.Checksum, bool) {
				calls++
				return object.Checksum{Algorithm: object.ChecksumCRC32C, Value: be32(checkCRC32C)}, true
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := drain(t, b, 64); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("Read after success = %v", err)
		}
		if calls != 1 {
			t.Fatalf("trailer called %d times, want 1", calls)
		}
	})
	t.Run("earlier failure", func(t *testing.T) {
		calls := 0
		b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{
			Size:       int64(len(checkInput)),
			ContentMD5: make([]byte, md5.Size),
			Trailer: func() (object.Checksum, bool) {
				calls++
				return object.Checksum{}, false
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = drain(t, b, 64)
		requireCode(t, err, s3err.BadDigest)
		if calls != 0 {
			t.Fatalf("trailer called %d times after an earlier failure, want 0", calls)
		}
	})
}
