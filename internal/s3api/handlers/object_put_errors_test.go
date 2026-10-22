package handlers_test

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestPutObjectRejectsUnknownOrOversizedLength checks the length gate of
// PutObject. A body of unknown length is refused with 411, and a declared
// length above the single-PUT limit is refused with 400 before the rest of the
// body is read. The oversized request sends its first 16 KiB, as a client that
// starts a large upload does. The transport reads about 8 KiB ahead of the
// handler, so a client that sends nothing at all is answered only when the
// header timeout expires.
func TestPutObjectRejectsUnknownOrOversizedLength(t *testing.T) {
	cases := []struct {
		name   string
		tail   string // header lines after Host and Authorization, then the body
		status int
		code   string
	}{
		{
			name:   "chunked body without Content-Length",
			tail:   "Transfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\n\r\n",
			status: http.StatusLengthRequired,
			code:   "MissingContentLength",
		},
		{
			name:   "declared length above the single PUT limit",
			tail:   "Content-Length: 5368709121\r\n\r\n" + strings.Repeat("0", 16<<10),
			status: http.StatusBadRequest,
			code:   "EntityTooLarge",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t, &fakes.Authorizer{})
			s.createBucket(t, "lengths")
			request := "PUT /lengths/k HTTP/1.1\r\nHost: " + s.addr + "\r\nAuthorization: test\r\n" + tc.tail
			r := s.raw(t, request)
			expectError(t, r, tc.status, tc.code)
			s.assertAbsent(t, "lengths", "k")
		})
	}
}

// TestPutObjectRejectsBadDigestAndStoresNothing checks that a body which fails
// its digest is refused with the matching S3 error, and that no version of the
// key is committed. The payload is verified while it is read, so the object
// must stay invisible to a later GET.
func TestPutObjectRejectsBadDigestAndStoresNothing(t *testing.T) {
	body := []byte("0123456789")
	otherMD5 := md5.Sum([]byte("other content"))
	wrongCRC := []byte{0xde, 0xad, 0xbe, 0xef}
	if got := binary.BigEndian.Uint32(wrongCRC); got == crc32.ChecksumIEEE(body) {
		t.Fatal("test digest accidentally matches the body")
	}

	cases := []struct {
		name   string
		header http.Header
		status int
		code   string
	}{
		{
			name:   "malformed Content-MD5",
			header: hdr("Content-MD5", "not-base64!"),
			status: http.StatusBadRequest,
			code:   "InvalidDigest",
		},
		{
			name:   "Content-MD5 of other bytes",
			header: hdr("Content-MD5", base64.StdEncoding.EncodeToString(otherMD5[:])),
			status: http.StatusBadRequest,
			code:   "BadDigest",
		},
		{
			name:   "x-amz-checksum-crc32 of other bytes",
			header: hdr("X-Amz-Checksum-Crc32", base64.StdEncoding.EncodeToString(wrongCRC)),
			status: http.StatusBadRequest,
			code:   "BadDigest",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newServer(t, &fakes.Authorizer{})
			s.createBucket(t, "digests")
			r := s.do(t, http.MethodPut, "/digests/k", body, tc.header)
			expectError(t, r, tc.status, tc.code)
			s.assertAbsent(t, "digests", "k")
		})
	}
}

// TestPutObjectDeniedByAuthorizer checks that a PUT the authorizer refuses
// fails with AccessDenied before the body is stored, and that the authorizer
// was consulted for this key.
func TestPutObjectDeniedByAuthorizer(t *testing.T) {
	deny := &fakes.Authorizer{Deny: func(_ auth.Identity, action auth.Action, _, _ string) bool {
		return action == auth.ActionPutObject
	}}
	s := newServer(t, deny)
	s.createBucket(t, "locked")

	r := s.do(t, http.MethodPut, "/locked/k", []byte("x"), nil)
	expectError(t, r, http.StatusForbidden, "AccessDenied")
	s.assertAbsent(t, "locked", "k")

	for _, c := range deny.Calls() {
		if c.Action == auth.ActionPutObject && c.Bucket == "locked" && c.Key == "k" {
			return
		}
	}
	t.Fatalf("authorizer was not asked about PutObject on locked/k; calls %+v", deny.Calls())
}
