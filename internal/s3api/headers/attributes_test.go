package headers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseObjectAttributes(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want object.PutOptions
		code s3err.Code
	}{
		{
			name: "empty header defaults the storage class",
			h:    http.Header{},
			want: object.PutOptions{StorageClass: "STANDARD"},
		},
		{
			name: "full header set",
			h: http.Header{
				"Content-Type":        {"text/plain"},
				"Content-Encoding":    {"gzip"},
				"Content-Disposition": {`attachment; filename="a.txt"`},
				"Content-Language":    {"en"},
				"Cache-Control":       {"max-age=60"},
				"Expires":             {"Wed, 21 Oct 2026 07:28:00 GMT"},
				"X-Amz-Meta-Owner":    {"bob"},
				"X-Amz-Tagging":       {"env=prod&team=a"},
				"X-Amz-Storage-Class": {"GLACIER_IR"},
				"X-Amz-Checksum-Crc32": {
					b64(0, 0, 0, 1),
				},
				"If-Match": {`"abc"`},
			},
			want: object.PutOptions{
				ContentType:        "text/plain",
				ContentEncoding:    "gzip",
				ContentDisposition: `attachment; filename="a.txt"`,
				ContentLanguage:    "en",
				CacheControl:       "max-age=60",
				Expires:            "Wed, 21 Oct 2026 07:28:00 GMT",
				UserMetadata:       map[string]string{"owner": "bob"},
				Tags:               "env=prod&team=a",
				StorageClass:       "GLACIER_IR",
				ChecksumAlgorithm:  object.ChecksumCRC32,
				Conditions:         object.Conditions{IfMatch: "abc"},
			},
		},
		{
			name: "checksum algorithm without digest",
			h:    http.Header{"X-Amz-Checksum-Algorithm": {"SHA256"}},
			want: object.PutOptions{StorageClass: "STANDARD", ChecksumAlgorithm: object.ChecksumSHA256},
		},
		{
			name: "attribute values are kept verbatim",
			h:    http.Header{"Content-Type": {"application/x-weird; charset=\"utf-8\""}},
			want: object.PutOptions{StorageClass: "STANDARD", ContentType: `application/x-weird; charset="utf-8"`},
		},

		{name: "lower case storage class", h: http.Header{"X-Amz-Storage-Class": {"standard"}}, code: s3err.InvalidStorageClass},
		{name: "unknown storage class", h: http.Header{"X-Amz-Storage-Class": {"COLD"}}, code: s3err.InvalidStorageClass},
		{name: "empty storage class", h: http.Header{"X-Amz-Storage-Class": {""}}, code: s3err.InvalidStorageClass},
		{
			name: "repeated storage class",
			h:    http.Header{"X-Amz-Storage-Class": {"STANDARD", "STANDARD_IA"}},
			code: s3err.InvalidStorageClass,
		},
		{name: "invalid tag", h: http.Header{"X-Amz-Tagging": {"a=1&a=2"}}, code: s3err.InvalidTag},
		{name: "invalid metadata", h: http.Header{"X-Amz-Meta-": {"x"}}, code: s3err.InvalidArgument},
		{name: "invalid checksum", h: http.Header{"X-Amz-Checksum-Crc32": {"AAAA"}}, code: s3err.InvalidRequest},
		{name: "invalid if-match", h: http.Header{"If-Match": {`"a", "b"`}}, code: s3err.InvalidArgument},
		{name: "control byte in content type", h: http.Header{"Content-Type": {"text/\x01plain"}}, code: s3err.InvalidArgument},
		{
			name: "cache control over the limit",
			h:    http.Header{"Cache-Control": {strings.Repeat("c", maxAttributeLen+1)}},
			code: s3err.InvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseObjectAttributes(tc.h)
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

func TestParseObjectAttributesStorageClasses(t *testing.T) {
	for _, sc := range []string{
		"STANDARD", "REDUCED_REDUNDANCY", "STANDARD_IA", "ONEZONE_IA",
		"INTELLIGENT_TIERING", "GLACIER", "DEEP_ARCHIVE", "GLACIER_IR",
	} {
		got, err := ParseObjectAttributes(http.Header{"X-Amz-Storage-Class": {sc}})
		wantNoError(t, err)
		if got.StorageClass != sc {
			t.Fatalf("storage class %s: got %q", sc, got.StorageClass)
		}
	}
}
