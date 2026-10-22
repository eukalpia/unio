package payload

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// readCase is one scripted Read scenario.
type readCase struct {
	name  string
	steps []step
	spec  Spec
	want  string     // payload delivered on success
	code  s3err.Code // expected failure code; empty for success
	same  error      // when set, the failure must be this exact value
	cause error      // when set, the failure must wrap this value
}

// readCases returns the scenarios for TestBodyRead. The table is built fresh on
// every call so that no state is shared between runs.
func readCases() []readCase {
	data := checkInput
	size := int64(len(data))
	whole := []step{{data: data, err: io.EOF}}
	md5sum := checkMD5(data)
	shasum := sha256.Sum256([]byte(data))
	wrongMD5 := make([]byte, md5.Size)
	wrongSHA := make([]byte, sha256.Size)
	reset := errors.New("connection reset")
	slow := s3err.Errorf(s3err.SlowDown, "slow down")

	cases := []readCase{
		{name: "exact payload", steps: whole, spec: Spec{Size: size}, want: data},
		{name: "chunks with eof on last chunk", steps: []step{{data: "12345"}, {data: "6789", err: io.EOF}}, spec: Spec{Size: size}, want: data},
		{name: "chunks with eof after last chunk", steps: []step{{data: "12345"}, {data: "6789"}}, spec: Spec{Size: size}, want: data},
		{name: "empty reads before data", steps: []step{{}, {data: data}, {}, {err: io.EOF}}, spec: Spec{Size: size}, want: data},
		{name: "zero length payload", spec: Spec{Size: 0}},
		{name: "zero length payload with matching content-md5", spec: Spec{ContentMD5: checkMD5("")}},
		{name: "zero length payload with wrong content-md5", spec: Spec{ContentMD5: wrongMD5}, code: s3err.BadDigest},
		{name: "content-md5 match", steps: whole, spec: Spec{Size: size, ContentMD5: md5sum}, want: data},
		{name: "content-md5 mismatch", steps: whole, spec: Spec{Size: size, ContentMD5: wrongMD5}, code: s3err.BadDigest},
		{name: "sha256 match", steps: whole, spec: Spec{Size: size, SHA256: shasum[:]}, want: data},
		{name: "sha256 mismatch", steps: whole, spec: Spec{Size: size, SHA256: wrongSHA}, code: s3err.XAmzContentSHA256Mismatch},
		{name: "content-md5 checked before sha256", steps: whole, spec: Spec{Size: size, ContentMD5: wrongMD5, SHA256: wrongSHA}, code: s3err.BadDigest},
		{name: "short payload with eof", steps: []step{{data: data[:8], err: io.EOF}}, spec: Spec{Size: size}, code: s3err.IncompleteBody},
		{name: "short payload without reader error", steps: []step{{data: data[:8]}}, spec: Spec{Size: size}, code: s3err.IncompleteBody},
		{name: "long payload with eof", steps: []step{{data: data + "0", err: io.EOF}}, spec: Spec{Size: size}, code: s3err.InvalidRequest},
		{name: "extra byte after declared length", steps: []step{{data: data}, {data: "0"}}, spec: Spec{Size: size}, code: s3err.InvalidRequest},
		{name: "extra byte with eof after declared length", steps: []step{{data: data}, {data: "0", err: io.EOF}}, spec: Spec{Size: size}, code: s3err.InvalidRequest},
		{name: "zero length payload with extra byte", steps: []step{{data: "x", err: io.EOF}}, spec: Spec{Size: 0}, code: s3err.InvalidRequest},
		{name: "unclassified reader error", steps: []step{{data: data[:4], err: reset}}, spec: Spec{Size: size}, code: s3err.IncompleteBody, cause: reset},
		{name: "unexpected eof from reader", steps: []step{{data: data[:4], err: io.ErrUnexpectedEOF}}, spec: Spec{Size: size}, code: s3err.IncompleteBody, cause: io.ErrUnexpectedEOF},
		{name: "classified reader error passes through", steps: []step{{data: data[:4], err: slow}}, spec: Spec{Size: size}, code: s3err.SlowDown, same: slow},
		{name: "context deadline passes through", steps: []step{{err: context.DeadlineExceeded}}, spec: Spec{Size: size}, code: s3err.RequestTimeout, same: context.DeadlineExceeded},
		{name: "trailer absent", steps: whole, spec: Spec{Size: size, Trailer: trailerOf(object.Checksum{}, false)}, want: data},
		{name: "trailer without algorithm", steps: whole, spec: Spec{Size: size, Trailer: trailerOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Value: be32(checkCRC32C)}, true)}, code: s3err.InvalidRequest},
		{name: "trailer match", steps: whole, spec: Spec{Size: size, Algorithm: object.ChecksumCRC32C, Trailer: trailerOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Value: be32(checkCRC32C)}, true)}, want: data},
		{name: "trailer value mismatch", steps: whole, spec: Spec{Size: size, Algorithm: object.ChecksumCRC32C, Trailer: trailerOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Value: corrupt(be32(checkCRC32C))}, true)}, code: s3err.XAmzContentChecksumMismatch},
		{name: "trailer algorithm mismatch", steps: whole, spec: Spec{Size: size, Algorithm: object.ChecksumCRC32C, Trailer: trailerOf(object.Checksum{Algorithm: object.ChecksumSHA1, Value: checkDigest(object.ChecksumSHA1)}, true)}, code: s3err.XAmzContentChecksumMismatch},
		{name: "expected checked before trailer", steps: whole, spec: Spec{Size: size, Algorithm: object.ChecksumCRC32C, Expected: corrupt(be32(checkCRC32C)), Trailer: trailerOf(object.Checksum{Algorithm: object.ChecksumCRC32C, Value: be32(checkCRC32C)}, true)}, code: s3err.BadDigest},
	}
	for _, a := range algorithms() {
		cases = append(cases,
			readCase{name: a.String() + " match", steps: whole, spec: Spec{Size: size, Algorithm: a, Expected: checkDigest(a)}, want: data},
			readCase{name: a.String() + " mismatch", steps: whole, spec: Spec{Size: size, Algorithm: a, Expected: corrupt(checkDigest(a))}, code: s3err.BadDigest},
			readCase{name: a.String() + " computed without expected", steps: whole, spec: Spec{Size: size, Algorithm: a}, want: data},
			readCase{name: a.String() + " chunked", steps: []step{{data: data[:2]}, {data: data[2:]}, {err: io.EOF}}, spec: Spec{Size: size, Algorithm: a, Expected: checkDigest(a)}, want: data},
			readCase{name: a.String() + " trailer match", steps: whole, spec: Spec{Size: size, Algorithm: a, Trailer: trailerOf(object.Checksum{Algorithm: a, Value: checkDigest(a)}, true)}, want: data},
			readCase{name: a.String() + " trailer mismatch", steps: whole, spec: Spec{Size: size, Algorithm: a, Trailer: trailerOf(object.Checksum{Algorithm: a, Value: corrupt(checkDigest(a))}, true)}, code: s3err.XAmzContentChecksumMismatch},
		)
	}
	return cases
}

func TestBodyRead(t *testing.T) {
	for _, tc := range readCases() {
		for _, bufSize := range []int{1, 3, 64} {
			t.Run(fmt.Sprintf("%s/buf=%d", tc.name, bufSize), func(t *testing.T) {
				b, err := New(newScript(tc.steps...), tc.spec)
				if err != nil {
					t.Fatal(err)
				}
				got, err := drain(t, b, bufSize)
				requireCode(t, err, tc.code)
				if tc.same != nil && err != tc.same {
					t.Fatalf("error = %v, want the reader's error unchanged", err)
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Fatalf("error %v does not wrap %v", err, tc.cause)
				}
				if tc.code == "" && string(got) != tc.want {
					t.Fatalf("delivered %q, want %q", got, tc.want)
				}
			})
		}
	}
}
