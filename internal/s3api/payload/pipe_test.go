package payload

import (
	"bytes"
	"crypto/md5" // #nosec G501 -- MD5 is the S3 ETag digest under test
	"crypto/sha256"
	"io"
	"math/rand/v2"
	"testing"
	"testing/iotest"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func largePayload(t *testing.T, size int) []byte {
	t.Helper()
	data := make([]byte, size)
	r := rand.NewChaCha8([32]byte{7})
	_, _ = r.Read(data)
	return data
}

// Sizes around the slab and threshold edges, read in odd chunk sizes, must
// give the same digests as hashing the whole payload at once.
func TestPipedDigestsMatchDirectHashing(t *testing.T) {
	for _, size := range []int{pipeThreshold, pipeThreshold + 1, 5*slabSize + 17, 5 << 20} {
		data := largePayload(t, size)
		sum := sha256.Sum256(data)
		m := md5.Sum(data) // #nosec G401 -- S3 ETag
		ck := sha256.Sum256(data)
		b, err := New(iotest.HalfReader(bytes.NewReader(data)), Spec{
			Size: int64(size), ContentMD5: m[:], SHA256: sum[:],
			Algorithm: object.ChecksumSHA256, Expected: ck[:],
		})
		if err != nil {
			t.Fatal(err)
		}
		if b.pipe == nil {
			t.Fatalf("size %d: payload hashed inline", size)
		}
		got, err := io.ReadAll(b)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("size %d: bytes changed", size)
		}
		if b.MD5() != m {
			t.Fatalf("size %d: MD5 differs", size)
		}
	}
}

// A large payload whose bytes do not match the signed SHA-256 must fail with
// XAmzContentSHA256Mismatch and never report io.EOF, since io.EOF is what lets
// the engine commit.
func TestPipedSHA256MismatchIsRejected(t *testing.T) {
	data := largePayload(t, 4<<20)
	sum := sha256.Sum256(data)
	data[len(data)/2] ^= 1
	b, err := New(bytes.NewReader(data), Spec{Size: int64(len(data)), SHA256: sum[:]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(io.Discard, b)
	if s3err.CodeOf(err) != s3err.XAmzContentSHA256Mismatch {
		t.Fatalf("got %v, want XAmzContentSHA256Mismatch", err)
	}
}

func TestPipedBadContentMD5IsRejected(t *testing.T) {
	data := largePayload(t, 2<<20)
	wrong := md5.Sum(data[1:]) // #nosec G401 -- S3 ETag
	b, err := New(bytes.NewReader(data), Spec{Size: int64(len(data)), ContentMD5: wrong[:]})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, b); s3err.CodeOf(err) != s3err.BadDigest {
		t.Fatalf("got %v, want BadDigest", err)
	}
}

// A Body abandoned midway must not leave goroutines blocked on it.
func TestPipedAbandonedBodyLeavesNoWork(t *testing.T) {
	data := largePayload(t, 8<<20)
	b, err := New(bytes.NewReader(data), Spec{Size: int64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3*slabSize)
	if _, err := io.ReadFull(b, buf); err != nil {
		t.Fatal(err)
	}
	// Whatever was handed over finishes on its own.
	<-b.pipe.tail
}
