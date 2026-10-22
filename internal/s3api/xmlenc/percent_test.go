package xmlenc

import "testing"

func TestAppendPercent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "unreserved kept", in: "AZaz09-._~/", want: "AZaz09-._~/"},
		{name: "space", in: "a b", want: "a%20b"},
		{name: "plus is encoded not spaced", in: "a+b", want: "a%2Bb"},
		{name: "reserved characters", in: "a&b=c?d#e", want: "a%26b%3Dc%3Fd%23e"},
		{name: "percent sign", in: "100%", want: "100%25"},
		{name: "xml specials", in: `<>"'`, want: "%3C%3E%22%27"},
		{name: "two byte utf8", in: "é", want: "%C3%A9"},
		{name: "four byte utf8", in: "😀", want: "%F0%9F%98%80"},
		{name: "invalid utf8 and nul", in: "\xff\x00", want: "%FF%00"},
		{name: "crlf", in: "a\r\n", want: "a%0D%0A"},
		{name: "path with spaces", in: "dir/sub dir/file.txt", want: "dir/sub%20dir/file.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendPercent(dst, tc.in) })
		})
	}
}
