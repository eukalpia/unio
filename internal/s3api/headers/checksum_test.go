package headers

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// b64 returns the standard base64 encoding of b, as a header would carry it.
func b64(b ...byte) string { return base64.StdEncoding.EncodeToString(b) }

func digestOf(n int) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = byte(i + 1)
	}
	return d
}

func TestParseChecksumRequest(t *testing.T) {
	crc32Digest := []byte{0, 0, 0, 1}
	cases := []struct {
		name string
		h    http.Header
		want ChecksumRequest
		code s3err.Code
	}{
		{name: "none", h: http.Header{}, want: ChecksumRequest{}},
		{
			name: "crc32 digest",
			h:    http.Header{"X-Amz-Checksum-Crc32": {b64(crc32Digest...)}},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC32, Expected: crc32Digest},
		},
		{
			name: "crc32c digest",
			h:    http.Header{"X-Amz-Checksum-Crc32c": {b64(0xde, 0xad, 0xbe, 0xef)}},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC32C, Expected: []byte{0xde, 0xad, 0xbe, 0xef}},
		},
		{
			name: "crc64nvme digest",
			h:    http.Header{"X-Amz-Checksum-Crc64nvme": {b64(digestOf(8)...)}},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC64NVME, Expected: digestOf(8)},
		},
		{
			name: "sha1 digest",
			h:    http.Header{"X-Amz-Checksum-Sha1": {b64(digestOf(20)...)}},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA1, Expected: digestOf(20)},
		},
		{
			name: "sha256 digest",
			h:    http.Header{"X-Amz-Checksum-Sha256": {b64(digestOf(32)...)}},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA256, Expected: digestOf(32)},
		},
		{
			name: "lower case header name in map",
			h:    http.Header{"x-amz-checksum-crc32": {b64(crc32Digest...)}},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC32, Expected: crc32Digest},
		},
		{
			name: "sdk algorithm without digest",
			h:    http.Header{"X-Amz-Sdk-Checksum-Algorithm": {"CRC32"}},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC32},
		},
		{
			name: "create multipart algorithm without digest",
			h:    http.Header{"X-Amz-Checksum-Algorithm": {"SHA256"}},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA256},
		},
		{
			name: "trailer names the algorithm",
			h:    http.Header{"X-Amz-Trailer": {"x-amz-checksum-crc32c"}},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC32C},
		},
		{
			name: "trailer list with other names",
			h:    http.Header{"X-Amz-Trailer": {"x-amz-meta-owner, x-amz-checksum-sha1"}},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA1},
		},
		{
			name: "trailer repeated in two lines",
			h:    http.Header{"X-Amz-Trailer": {"x-amz-checksum-sha1", "X-Amz-Checksum-Sha1"}},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA1},
		},
		{
			name: "trailer without checksum names",
			h:    http.Header{"X-Amz-Trailer": {"x-amz-meta-owner"}},
			want: ChecksumRequest{},
		},
		{
			name: "algorithm and digest agree",
			h: http.Header{
				"X-Amz-Checksum-Algorithm": {"CRC32"},
				"X-Amz-Checksum-Crc32":     {b64(crc32Digest...)},
			},
			want: ChecksumRequest{Algorithm: object.ChecksumCRC32, Expected: crc32Digest},
		},
		{
			name: "both algorithm headers agree",
			h: http.Header{
				"X-Amz-Sdk-Checksum-Algorithm": {"SHA1"},
				"X-Amz-Checksum-Algorithm":     {"SHA1"},
			},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA1},
		},
		{
			name: "digest and trailer agree",
			h: http.Header{
				"X-Amz-Checksum-Sha256": {b64(digestOf(32)...)},
				"X-Amz-Trailer":         {"x-amz-checksum-sha256"},
			},
			want: ChecksumRequest{Algorithm: object.ChecksumSHA256, Expected: digestOf(32)},
		},
		{
			name: "checksum mode and type are not digests",
			h: http.Header{
				"X-Amz-Checksum-Mode": {"ENABLED"},
				"X-Amz-Checksum-Type": {"FULL_OBJECT"},
			},
			want: ChecksumRequest{},
		},

		{name: "digest is not base64", h: http.Header{"X-Amz-Checksum-Crc32": {"!!!!"}}, code: s3err.InvalidRequest},
		{name: "digest is empty", h: http.Header{"X-Amz-Checksum-Crc32": {""}}, code: s3err.InvalidRequest},
		{name: "crc32 digest too long", h: http.Header{"X-Amz-Checksum-Crc32": {b64(digestOf(8)...)}}, code: s3err.InvalidRequest},
		{name: "sha256 digest too short", h: http.Header{"X-Amz-Checksum-Sha256": {b64(digestOf(20)...)}}, code: s3err.InvalidRequest},
		{name: "digest without padding", h: http.Header{"X-Amz-Checksum-Crc32": {"AAAAAQ"}}, code: s3err.InvalidRequest},
		{name: "digest with non-zero padding bits", h: http.Header{"X-Amz-Checksum-Crc32": {"AAAAAR=="}}, code: s3err.InvalidRequest},
		{
			name: "two digest headers",
			h: http.Header{
				"X-Amz-Checksum-Crc32": {b64(crc32Digest...)},
				"X-Amz-Checksum-Sha1":  {b64(digestOf(20)...)},
			},
			code: s3err.InvalidRequest,
		},
		{
			name: "same digest header repeated",
			h:    http.Header{"X-Amz-Checksum-Crc32": {b64(crc32Digest...), b64(crc32Digest...)}},
			code: s3err.InvalidRequest,
		},
		{name: "unknown digest algorithm", h: http.Header{"X-Amz-Checksum-Md5": {b64(digestOf(16)...)}}, code: s3err.InvalidRequest},
		{name: "lower case algorithm name", h: http.Header{"X-Amz-Checksum-Algorithm": {"crc32"}}, code: s3err.InvalidRequest},
		{name: "unknown algorithm name", h: http.Header{"X-Amz-Checksum-Algorithm": {"MD5"}}, code: s3err.InvalidRequest},
		{name: "empty algorithm value", h: http.Header{"X-Amz-Checksum-Algorithm": {""}}, code: s3err.InvalidRequest},
		{
			name: "repeated algorithm header",
			h:    http.Header{"X-Amz-Checksum-Algorithm": {"CRC32", "CRC32"}},
			code: s3err.InvalidRequest,
		},
		{
			name: "sdk and checksum algorithm disagree",
			h: http.Header{
				"X-Amz-Sdk-Checksum-Algorithm": {"CRC32"},
				"X-Amz-Checksum-Algorithm":     {"SHA1"},
			},
			code: s3err.InvalidRequest,
		},
		{
			name: "algorithm and digest disagree",
			h: http.Header{
				"X-Amz-Checksum-Algorithm": {"CRC32"},
				"X-Amz-Checksum-Sha1":      {b64(digestOf(20)...)},
			},
			code: s3err.InvalidRequest,
		},
		{
			name: "trailer disagrees with algorithm",
			h: http.Header{
				"X-Amz-Checksum-Algorithm": {"CRC32"},
				"X-Amz-Trailer":            {"x-amz-checksum-sha1"},
			},
			code: s3err.InvalidRequest,
		},
		{
			name: "trailer names two algorithms",
			h:    http.Header{"X-Amz-Trailer": {"x-amz-checksum-crc32, x-amz-checksum-sha1"}},
			code: s3err.InvalidRequest,
		},
		{
			name: "trailer names unknown algorithm",
			h:    http.Header{"X-Amz-Trailer": {"x-amz-checksum-md5"}},
			code: s3err.InvalidRequest,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChecksumRequest(tc.h)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				return
			}
			wantNoError(t, err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseChecksumMode(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want bool
	}{
		{name: "absent", h: http.Header{}, want: false},
		{name: "enabled", h: http.Header{"X-Amz-Checksum-Mode": {"ENABLED"}}, want: true},
		{name: "lower case", h: http.Header{"X-Amz-Checksum-Mode": {"enabled"}}, want: true},
		{name: "padded", h: http.Header{"X-Amz-Checksum-Mode": {" ENABLED "}}, want: true},
		{name: "disabled", h: http.Header{"X-Amz-Checksum-Mode": {"DISABLED"}}, want: false},
		{name: "empty", h: http.Header{"X-Amz-Checksum-Mode": {""}}, want: false},
		{name: "prefix only", h: http.Header{"X-Amz-Checksum-Mode": {"ENABLEDX"}}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseChecksumMode(tc.h); got != tc.want {
				t.Fatalf("ParseChecksumMode = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseContentMD5(t *testing.T) {
	sum := digestOf(16)
	cases := []struct {
		name string
		h    http.Header
		want []byte
		code s3err.Code
	}{
		{name: "absent", h: http.Header{}, want: nil},
		{name: "valid", h: http.Header{"Content-Md5": {b64(sum...)}}, want: sum},
		{name: "empty value", h: http.Header{"Content-Md5": {""}}, code: s3err.InvalidDigest},
		{name: "not base64", h: http.Header{"Content-Md5": {"not base64!"}}, code: s3err.InvalidDigest},
		{name: "15 bytes", h: http.Header{"Content-Md5": {b64(digestOf(15)...)}}, code: s3err.InvalidDigest},
		{name: "17 bytes", h: http.Header{"Content-Md5": {b64(digestOf(17)...)}}, code: s3err.InvalidDigest},
		{name: "hex instead of base64", h: http.Header{"Content-Md5": {"d41d8cd98f00b204e9800998ecf8427e"}}, code: s3err.InvalidDigest},
		{name: "non-zero padding bits", h: http.Header{"Content-Md5": {"1vHwu3f5a0mhGm0gQ6AzPu=="}}, code: s3err.InvalidDigest},
		{name: "repeated header", h: http.Header{"Content-Md5": {b64(sum...), b64(sum...)}}, code: s3err.InvalidDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseContentMD5(tc.h)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				return
			}
			wantNoError(t, err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
