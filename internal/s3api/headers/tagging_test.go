package headers

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestParseTagging(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		code s3err.Code
	}{
		{name: "empty", in: "", want: ""},
		{name: "single", in: "k=v", want: "k=v"},
		{name: "keys are sorted", in: "b=2&a=1", want: "a=1&b=2"},
		{name: "plus is a space", in: "a+b=c+d", want: "a+b=c+d"},
		{name: "percent space is canonical plus", in: "a=%20x", want: "a=+x"},
		{name: "key without value", in: "flag", want: "flag="},
		{name: "empty value", in: "k=", want: "k="},
		{name: "equals inside value", in: "k=a=b", want: "k=a%3Db"},
		{name: "encoded equals in key", in: "%3D=v", want: "%3D=v"},
		{name: "utf-8 key and value", in: "%D0%BA=%D0%B7", want: "%D0%BA=%D0%B7"},
		{name: "ten tags", in: tagList(object.MaxTags), want: tagList(object.MaxTags)},
		{name: "key at the length limit", in: strings.Repeat("k", maxTagKeyLen) + "=v", want: strings.Repeat("k", maxTagKeyLen) + "=v"},
		{name: "value at the length limit", in: "k=" + strings.Repeat("v", maxTagValueLen), want: "k=" + strings.Repeat("v", maxTagValueLen)},

		{name: "malformed escape", in: "a=%ZZ", code: s3err.InvalidTag},
		{name: "truncated escape in key", in: "a%2=1", code: s3err.InvalidTag},
		{name: "duplicate key", in: "a=1&a=2", code: s3err.InvalidTag},
		{name: "duplicate key after decoding", in: "a=1&%61=2", code: s3err.InvalidTag},
		{name: "empty key", in: "=v", code: s3err.InvalidTag},
		{name: "empty pair in middle", in: "a=1&&b=2", code: s3err.InvalidTag},
		{name: "trailing ampersand", in: "a=1&", code: s3err.InvalidTag},
		{name: "eleven tags", in: tagList(object.MaxTags + 1), code: s3err.InvalidTag},
		{name: "key over the limit", in: strings.Repeat("k", maxTagKeyLen+1) + "=v", code: s3err.InvalidTag},
		{name: "value over the limit", in: "k=" + strings.Repeat("v", maxTagValueLen+1), code: s3err.InvalidTag},
		{name: "invalid utf-8 key", in: "%FF=v", code: s3err.InvalidTag},
		{name: "invalid utf-8 value", in: "k=%C3", code: s3err.InvalidTag},
		{name: "control byte in value", in: "k=%00", code: s3err.InvalidTag},
		{name: "tab in key", in: "a%09b=v", code: s3err.InvalidTag},
		{name: "too many pairs in raw input", in: strings.Repeat("a=b&", 3000), code: s3err.InvalidTag},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseTagging(tc.in)
			if tc.code != "" {
				wantCode(t, err, tc.code)
				return
			}
			wantNoError(t, err)
			if got != tc.want {
				t.Fatalf("ParseTagging(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// tagList returns n tags k00=v, k01=v, ... joined with '&'.
func tagList(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "k" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "=v"
	}
	return strings.Join(parts, "&")
}
