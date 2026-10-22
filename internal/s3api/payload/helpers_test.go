package payload

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"slices"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// checkInput is the input whose CRC digests are the published check values.
const checkInput = "123456789"

// Published check values of checkInput for the CRC algorithms.
const (
	checkCRC32     uint32 = 0xCBF43926
	checkCRC32C    uint32 = 0xE3069283
	checkCRC64NVME uint64 = 0xAE8B14860A799888
)

// maxReads bounds drain so that a broken Body fails the test instead of
// hanging it.
const maxReads = 1 << 20

// algorithms lists every checksum algorithm that a Body can compute.
func algorithms() []object.ChecksumAlgorithm {
	return []object.ChecksumAlgorithm{
		object.ChecksumCRC32,
		object.ChecksumCRC32C,
		object.ChecksumCRC64NVME,
		object.ChecksumSHA1,
		object.ChecksumSHA256,
	}
}

// checkDigest returns the big-endian digest of checkInput for a.
func checkDigest(a object.ChecksumAlgorithm) []byte {
	switch a {
	case object.ChecksumCRC32:
		return be32(checkCRC32)
	case object.ChecksumCRC32C:
		return be32(checkCRC32C)
	case object.ChecksumCRC64NVME:
		return be64(checkCRC64NVME)
	case object.ChecksumSHA1:
		d := sha1.Sum([]byte(checkInput))
		return d[:]
	case object.ChecksumSHA256:
		d := sha256.Sum256([]byte(checkInput))
		return d[:]
	}
	return nil
}

// checkMD5 returns the MD5 digest of s as a fresh slice.
func checkMD5(s string) []byte {
	d := md5.Sum([]byte(s))
	return d[:]
}

func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func be64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

// corrupt returns a copy of d with its last bit flipped.
func corrupt(d []byte) []byte {
	c := bytes.Clone(d)
	c[len(c)-1] ^= 1
	return c
}

// trailerOf returns a Trailer function that always reports c and ok.
func trailerOf(c object.Checksum, ok bool) func() (object.Checksum, bool) {
	return func() (object.Checksum, bool) { return c, ok }
}

// step is one scripted result of an underlying reader.
type step struct {
	data string
	err  error
}

// scriptReader replays its steps in order and then reports io.EOF. A step
// whose data does not fit the caller's buffer continues on the next call,
// keeping its error.
type scriptReader struct {
	steps []step
}

func newScript(steps ...step) *scriptReader {
	return &scriptReader{steps: slices.Clone(steps)}
}

func (s *scriptReader) Read(p []byte) (int, error) {
	if len(s.steps) == 0 {
		return 0, io.EOF
	}
	st := s.steps[0]
	n := copy(p, st.data)
	if n < len(st.data) {
		s.steps[0] = step{data: st.data[n:], err: st.err}
		return n, nil
	}
	s.steps = s.steps[1:]
	return n, st.err
}

// recordingReader records the length of every buffer it is offered.
type recordingReader struct {
	r    io.Reader
	caps []int
}

func (r *recordingReader) Read(p []byte) (int, error) {
	r.caps = append(r.caps, len(p))
	return r.r.Read(p)
}

// zeroReader yields an endless stream of zero bytes.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// drain reads b with buffers of bufSize bytes until a terminal result. It
// returns the delivered bytes and the failure, with io.EOF reported as nil.
// It also checks that the terminal result is sticky.
func drain(t *testing.T, b *Body, bufSize int) ([]byte, error) {
	t.Helper()
	buf := make([]byte, bufSize)
	var out []byte
	for range maxReads {
		n, err := b.Read(buf)
		out = append(out, buf[:n]...)
		if err == nil {
			continue
		}
		if again, err2 := b.Read(buf); again != 0 || err2 != err {
			t.Fatalf("terminal result not sticky: got (%d, %v) after %v", again, err2, err)
		}
		if err == io.EOF {
			return out, nil
		}
		return out, err
	}
	t.Fatal("body never reached a terminal result")
	return nil, nil
}

// requireCode fails the test unless err carries the S3 code want. An empty
// want requires err to be nil.
func requireCode(t *testing.T, err error, want s3err.Code) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if got := s3err.CodeOf(err); got != want {
		t.Fatalf("error code = %q (err: %v), want %q", got, err, want)
	}
}
