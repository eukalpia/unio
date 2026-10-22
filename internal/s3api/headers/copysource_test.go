package headers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseCopySource(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want object.Ref
		code s3err.Code
	}{
		{name: "bucket and key", in: "bucket/key", want: object.Ref{Bucket: "bucket", Key: "key"}},
		{name: "leading slash", in: "/bucket/key", want: object.Ref{Bucket: "bucket", Key: "key"}},
		{name: "surrounding spaces", in: "  bucket/key  ", want: object.Ref{Bucket: "bucket", Key: "key"}},
		{name: "encoded key", in: "bucket/dir%2Fa%20b.txt", want: object.Ref{Bucket: "bucket", Key: "dir/a b.txt"}},
		{name: "slashes in key", in: "bucket/a/b/c", want: object.Ref{Bucket: "bucket", Key: "a/b/c"}},
		{name: "plus stays plus", in: "bucket/a+b", want: object.Ref{Bucket: "bucket", Key: "a+b"}},
		{name: "encoded question mark", in: "bucket/key%3Fx", want: object.Ref{Bucket: "bucket", Key: "key?x"}},
		{name: "utf-8 key", in: "bucket/%E2%82%AC", want: object.Ref{Bucket: "bucket", Key: "€"}},
		{
			name: "version id",
			in:   "bucket/key?versionId=3L4kqtJlcpXroDTDmJ",
			want: object.Ref{Bucket: "bucket", Key: "key", VersionID: "3L4kqtJlcpXroDTDmJ"},
		},
		{
			name: "version id with dot dash and underscore",
			in:   "/bucket/key?versionId=v1.2-a_b",
			want: object.Ref{Bucket: "bucket", Key: "key", VersionID: "v1.2-a_b"},
		},

		{name: "empty", in: "", code: s3err.InvalidArgument},
		{name: "only slash", in: "/", code: s3err.InvalidArgument},
		{name: "bucket only", in: "bucket", code: s3err.InvalidArgument},
		{name: "missing key", in: "bucket/", code: s3err.InvalidArgument},
		{name: "missing key with leading slash", in: "/bucket/", code: s3err.InvalidArgument},
		{name: "missing bucket", in: "//key", code: s3err.InvalidArgument},
		{name: "bucket decodes to blanks", in: "%20/key", code: s3err.InvalidBucketName},
		{name: "malformed escape in key", in: "bucket/a%zz", code: s3err.InvalidArgument},
		{name: "malformed escape in bucket", in: "buck%et/key", code: s3err.InvalidArgument},
		{name: "upper case bucket", in: "Bucket/key", code: s3err.InvalidBucketName},
		{name: "short bucket", in: "ab/key", code: s3err.InvalidBucketName},
		{name: "encoded slash in bucket", in: "b%2Fxyz/key", code: s3err.InvalidBucketName},
		{name: "key with nul", in: "bucket/a%00b", code: s3err.InvalidArgument},
		{name: "key with invalid utf-8", in: "bucket/%FF", code: s3err.InvalidArgument},
		{name: "key too long", in: "bucket/" + strings.Repeat("a", object.MaxKeyLength+1), code: s3err.KeyTooLongError},
		{name: "raw question mark starts a bad query", in: "bucket/a?b", code: s3err.InvalidArgument},
		{name: "wrong parameter name", in: "bucket/key?versionid=1", code: s3err.InvalidArgument},
		{name: "other parameter", in: "bucket/key?foo=bar", code: s3err.InvalidArgument},
		{name: "empty version id", in: "bucket/key?versionId=", code: s3err.InvalidArgument},
		{name: "version id with ampersand", in: "bucket/key?versionId=a&b", code: s3err.InvalidArgument},
		{name: "version id with percent", in: "bucket/key?versionId=a%2Fb", code: s3err.InvalidArgument},
		{name: "version id with space", in: "bucket/key?versionId=a b", code: s3err.InvalidArgument},
		{name: "version id too long", in: "bucket/key?versionId=" + strings.Repeat("v", maxVersionIDLen+1), code: s3err.InvalidArgument},
		{name: "raw input too long", in: "bucket/" + strings.Repeat("a%20", 2000), code: s3err.InvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCopySource(tc.in)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				return
			}
			wantNoError(t, err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("ParseCopySource(%q) mismatch (-want +got):\n%s", tc.in, diff)
			}
		})
	}
}

func TestParseMetadataDirective(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want object.MetadataDirective
		code s3err.Code
	}{
		{name: "absent defaults to copy", h: http.Header{}, want: object.MetadataCopy},
		{name: "copy", h: http.Header{"X-Amz-Metadata-Directive": {"COPY"}}, want: object.MetadataCopy},
		{name: "replace", h: http.Header{"X-Amz-Metadata-Directive": {"REPLACE"}}, want: object.MetadataReplace},
		{name: "lower case", h: http.Header{"X-Amz-Metadata-Directive": {"copy"}}, code: s3err.InvalidArgument},
		{name: "empty", h: http.Header{"X-Amz-Metadata-Directive": {""}}, code: s3err.InvalidArgument},
		{name: "unknown", h: http.Header{"X-Amz-Metadata-Directive": {"MERGE"}}, code: s3err.InvalidArgument},
		{name: "combined values", h: http.Header{"X-Amz-Metadata-Directive": {"COPY, REPLACE"}}, code: s3err.InvalidArgument},
		{
			name: "repeated header",
			h:    http.Header{"X-Amz-Metadata-Directive": {"REPLACE", "REPLACE"}},
			code: s3err.InvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseMetadataDirective(tc.h)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				return
			}
			wantNoError(t, err)
			if got != tc.want {
				t.Fatalf("ParseMetadataDirective = %v, want %v", got, tc.want)
			}
		})
	}
}
