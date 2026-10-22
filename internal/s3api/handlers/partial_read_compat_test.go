package handlers_test

import (
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestPartialReadCarriesNoChecksum checks that a range read answers without the
// checksum of the whole object, which would not match the bytes it sends, while
// a full read in checksum mode still carries it.
func TestPartialReadCarriesNoChecksum(t *testing.T) {
	s := newServer(t, &fakes.Authorizer{})
	s.createBucket(t, "sums")
	body := []byte("hello")
	sum := crc32.ChecksumIEEE(body)
	var digest [4]byte
	binary.BigEndian.PutUint32(digest[:], sum)
	put := s.do(t, http.MethodPut, "/sums/k", body, http.Header{
		"X-Amz-Checksum-Crc32": {base64.StdEncoding.EncodeToString(digest[:])},
	})
	if put.status != http.StatusOK {
		t.Fatalf("put with checksum: status %d, body %s", put.status, put.body)
	}

	full := s.do(t, http.MethodGet, "/sums/k", nil, http.Header{"X-Amz-Checksum-Mode": {"ENABLED"}})
	if full.header.Get("X-Amz-Checksum-Crc32") == "" {
		t.Fatal("full read in checksum mode has no checksum header")
	}

	part := s.do(t, http.MethodGet, "/sums/k", nil, http.Header{
		"X-Amz-Checksum-Mode": {"ENABLED"},
		"Range":               {"bytes=0-1"},
	})
	if part.status != http.StatusPartialContent {
		t.Fatalf("range read: status %d, want 206", part.status)
	}
	if got := part.header.Get("X-Amz-Checksum-Crc32"); got != "" {
		t.Fatalf("range read carries checksum %q of the whole object", got)
	}
}
