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
		{name: "expected before missing required trailer", spec: Spec{Algorithm: object.ChecksumCRC32C, Expected: badCRC, TrailerRequired: true}, code: s3err.BadDigest},
		{name: "missing required trailer last", spec: Spec{Algorithm: object.ChecksumCRC32C, Expected: be32(checkCRC32C), TrailerRequired: true}, code: s3err.IncompleteBody},
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

// TestTrailerRequiredSuccess checks that a declared trailing checksum with the
// right algorithm and digest completes the payload, for every algorithm.
func TestTrailerRequiredSuccess(t *testing.T) {
	for _, a := range algorithms() {
		t.Run(a.String(), func(t *testing.T) {
			b, err := New(newScript(step{data: checkInput, err: io.EOF}), Spec{
				Size:            int64(len(checkInput)),
				Algorithm:       a,
				TrailerRequired: true,
				Trailer:         trailerOf(object.Checksum{Algorithm: a, Value: checkDigest(a)}, true),
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := drain(t, b, 64)
			requireCode(t, err, "")
			if string(got) != checkInput {
				t.Fatalf("delivered %q, want %q", got, checkInput)
			}
			if c := b.Checksum(); c.Algorithm != a || !bytes.Equal(c.Value, checkDigest(a)) {
				t.Fatalf("Checksum() = %v with digest %x, want %s digest %x", c.Algorithm, c.Value, a, checkDigest(a))
			}
		})
	}
}

// TestTrailerRequiredFailsFinalRead checks that a missing trailing checksum is
// reported by the Read that reaches the end of the payload, never as io.EOF,
// and that the failure is sticky.
func TestTrailerRequiredFailsFinalRead(t *testing.T) {
	tests := []struct {
		name  string
		steps []step
	}{
		{name: "last bytes with eof", steps: []step{{data: checkInput, err: io.EOF}}},
		{name: "last bytes then separate eof", steps: []step{{data: checkInput}, {err: io.EOF}}},
		{name: "last bytes split", steps: []step{{data: checkInput[:5]}, {data: checkInput[5:], err: io.EOF}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, bufSize := range []int{1, 4, 64} {
				b, err := New(newScript(tc.steps...), Spec{
					Size:            int64(len(checkInput)),
					Algorithm:       object.ChecksumCRC32C,
					TrailerRequired: true,
					Trailer:         trailerOf(object.Checksum{}, false),
				})
				if err != nil {
					t.Fatal(err)
				}
				buf := make([]byte, bufSize)
				var delivered []byte
				var terminal error
				for range maxReads {
					n, err := b.Read(buf)
					delivered = append(delivered, buf[:n]...)
					if err != nil {
						terminal = err
						break
					}
				}
				if terminal == nil {
					t.Fatalf("buf=%d: no terminal error", bufSize)
				}
				requireCode(t, terminal, s3err.IncompleteBody)
				if string(delivered) != checkInput {
					t.Fatalf("buf=%d: delivered %q before the error, want %q", bufSize, delivered, checkInput)
				}
				if n, err := b.Read(buf); n != 0 || err != terminal {
					t.Fatalf("buf=%d: Read after failure = (%d, %v), want (0, %v)", bufSize, n, err, terminal)
				}
			}
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
