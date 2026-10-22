package xmlenc

import "testing"

func TestAppendEscape(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain ascii is unchanged", in: "photos/2026 summer.jpg", want: "photos/2026 summer.jpg"},
		{name: "ampersand", in: "a&b", want: "a&amp;b"},
		{name: "less than", in: "a<b", want: "a&lt;b"},
		{name: "greater than", in: "a>b", want: "a&gt;b"},
		{name: "double quote", in: `a"b`, want: "a&#34;b"},
		{name: "apostrophe", in: "a'b", want: "a&#39;b"},
		{name: "carriage return", in: "a\rb", want: "a&#13;b"},
		{name: "tab and newline are kept", in: "a\tb\nc", want: "a\tb\nc"},
		{name: "all special characters", in: `&<>"'` + "\r", want: "&amp;&lt;&gt;&#34;&#39;&#13;"},
		{name: "cdata terminator", in: "]]>", want: "]]&gt;"},
		{name: "nul is replaced", in: "a\x00b", want: "a�b"},
		{name: "c0 controls are replaced", in: "\x01\x08\x0b\x0c\x1f", want: "�����"},
		{name: "del is kept", in: "\x7f", want: "\x7f"},
		{name: "c1 control is kept", in: "\u0085", want: "\u0085"},
		{name: "invalid byte is replaced", in: "a\xffb", want: "a�b"},
		{name: "truncated sequence is replaced per byte", in: "a\xe2\x82", want: "a��"},
		{name: "encoded surrogate is replaced per byte", in: "\xed\xa0\x80", want: "���"},
		{name: "U+FFFE is replaced", in: "￾", want: "�"},
		{name: "U+FFFF is replaced", in: "￿", want: "�"},
		{name: "U+FFFD is kept", in: "�", want: "�"},
		{name: "emoji is kept", in: "key-😀.txt", want: "key-😀.txt"},
		{name: "highest code point is kept", in: "\U0010FFFF", want: "\U0010FFFF"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendEscape(dst, tc.in) })
		})
	}
}
