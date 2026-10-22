package headers

import "testing"

func TestQuoteETag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", `""`},
		{"d41d8cd98f00b204e9800998ecf8427e", `"d41d8cd98f00b204e9800998ecf8427e"`},
		{`"abc"`, `"abc"`},
		{`W/"abc"`, `W/"abc"`},
		{`"`, `"""`},
		{"*", `"*"`},
	}
	for _, c := range cases {
		if got := QuoteETag(c.in); got != c.want {
			t.Fatalf("QuoteETag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnquoteETag(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{`"abc"`, "abc"},
		{"abc", "abc"},
		{`  "abc"  `, "abc"},
		{`W/"abc"`, "abc"},
		{` W/"abc" `, "abc"},
		{"W/abc", "abc"},
		{`""`, ""},
		{`"`, `"`},
		{`""abc""`, `"abc"`},
		{`"a"b"`, `a"b`},
		{"*", "*"},
	}
	for _, c := range cases {
		if got := UnquoteETag(c.in); got != c.want {
			t.Fatalf("UnquoteETag(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestQuoteUnquoteRoundTrip(t *testing.T) {
	for _, etag := range []string{"abc", "d41d8cd9-2"} {
		if got := UnquoteETag(QuoteETag(etag)); got != etag {
			t.Fatalf("round trip of %q gave %q", etag, got)
		}
	}
}
