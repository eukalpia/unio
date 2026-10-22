package obs

import (
	"strings"
	"testing"
)

func TestSanitizeLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "GetObject", want: "GetObject"},
		{name: "allowed punctuation", in: "drive-1.a_b:c", want: "drive-1.a_b:c"},
		{name: "digits", in: "200", want: "200"},
		{name: "empty", in: "", want: "other"},
		{name: "exactly 64 bytes", in: strings.Repeat("a", 64), want: strings.Repeat("a", 64)},
		{name: "65 bytes", in: strings.Repeat("a", 65), want: "other"},
		{name: "space", in: "get object", want: "other"},
		{name: "slash", in: "/bucket/key", want: "other"},
		{name: "quote", in: `a"b`, want: "other"},
		{name: "newline", in: "a\nb", want: "other"},
		{name: "non ascii", in: "drïve", want: "other"},
		{name: "percent", in: "a%20b", want: "other"},
		{name: "braces", in: "{x}", want: "other"},
		{name: "multibyte counted as bytes", in: strings.Repeat("é", 33), want: "other"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeLabel(tc.in); got != tc.want {
				t.Fatalf("sanitizeLabel(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
