package headers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseUserMetadata(t *testing.T) {
	cases := []struct {
		name string
		h    http.Header
		want map[string]string
		code s3err.Code
	}{
		{name: "none", h: http.Header{}, want: nil},
		{
			name: "single",
			h:    http.Header{"X-Amz-Meta-Foo": {"bar"}},
			want: map[string]string{"foo": "bar"},
		},
		{
			name: "name keeps inner dashes and is lower cased",
			h:    http.Header{"X-Amz-Meta-Multi-Part": {"Value"}},
			want: map[string]string{"multi-part": "Value"},
		},
		{
			name: "first value only",
			h:    http.Header{"X-Amz-Meta-Foo": {"first", "second"}},
			want: map[string]string{"foo": "first"},
		},
		{
			name: "empty value is allowed",
			h:    http.Header{"X-Amz-Meta-Empty": {""}},
			want: map[string]string{"empty": ""},
		},
		{
			name: "metadata directive is not user metadata",
			h:    http.Header{"X-Amz-Metadata-Directive": {"REPLACE"}, "X-Amz-Meta-A": {"1"}},
			want: map[string]string{"a": "1"},
		},
		{
			name: "lower case key in map",
			h:    http.Header{"x-amz-meta-owner": {"bob"}},
			want: map[string]string{"owner": "bob"},
		},
		{
			name: "total size at the limit",
			h: http.Header{
				"X-Amz-Meta-K": {strings.Repeat("v", object.MaxUserMetadataSize-1)},
			},
			want: map[string]string{"k": strings.Repeat("v", object.MaxUserMetadataSize-1)},
		},
		{
			name: "total size over the limit",
			h: http.Header{
				"X-Amz-Meta-K": {strings.Repeat("v", object.MaxUserMetadataSize)},
			},
			code: s3err.MetadataTooLarge,
		},
		{
			name: "several entries over the limit",
			h: http.Header{
				"X-Amz-Meta-A": {strings.Repeat("a", 1000)},
				"X-Amz-Meta-B": {strings.Repeat("b", 1000)},
				"X-Amz-Meta-C": {strings.Repeat("c", 100)},
			},
			code: s3err.MetadataTooLarge,
		},
		{name: "empty name", h: http.Header{"X-Amz-Meta-": {"x"}}, code: s3err.InvalidArgument},
		{name: "control byte in value", h: http.Header{"X-Amz-Meta-A": {"x\x01y"}}, code: s3err.InvalidArgument},
		{
			name: "same name from two keys",
			h: http.Header{
				"X-Amz-Meta-A": {"1"},
				"x-amz-meta-a": {"2"},
			},
			code: s3err.InvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseUserMetadata(tc.h)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				if got != nil {
					t.Fatalf("want nil map on error, got %v", got)
				}
				return
			}
			wantNoError(t, err)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
