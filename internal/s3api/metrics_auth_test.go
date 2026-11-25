package s3api

import (
	"bytes"
	"testing"
)

func TestParseBearer(t *testing.T) {
	cases := []struct {
		in      string
		token   string
		bearer  bool
		comment string
	}{
		{in: "Bearer abc", token: "abc", bearer: true},
		{in: "bearer abc", token: "abc", bearer: true, comment: "scheme is case-insensitive"},
		{in: "BeArEr abc", token: "abc", bearer: true},
		{in: "Bearer   abc", token: "abc", bearer: true, comment: "1*SP between scheme and credentials"},
		{in: "Bearer ", token: "", bearer: true},
		{in: "Bearer", token: "", bearer: true},
		{in: "Bearerabc", token: "", bearer: true, comment: "no space: never a token"},
		{in: "Bearer\tabc", token: "", bearer: true, comment: "tab is not the separator"},
		{in: "Bearer abc def", token: "abc def", bearer: true},
		{in: "AWS4-HMAC-SHA256 Credential=x", bearer: false},
		{in: "Basic Ym9iOnBhc3M=", bearer: false},
		{in: "Beare abc", bearer: false},
		{in: "", bearer: false},
		{in: "Bear", bearer: false},
		{in: " Bearer abc", bearer: false, comment: "leading space is not the scheme"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			token, bearer := parseBearer([]byte(tc.in))
			if bearer != tc.bearer {
				t.Fatalf("bearer = %v, want %v", bearer, tc.bearer)
			}
			if string(token) != tc.token {
				t.Fatalf("token = %q, want %q", token, tc.token)
			}
		})
	}
}

// FuzzParseBearer checks the invariants the metrics endpoint relies on for
// any Authorization value: the parser does not panic, a non-Bearer value
// yields no token, and a token is always a tail of the value, never starting
// with the separator.
func FuzzParseBearer(f *testing.F) {
	for _, seed := range []string{"Bearer abc", "bearer", "BEARER  x", "AWS4-HMAC-SHA256 Credential=a", "", "Bearer\x00", "bEaReR\xff"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, v []byte) {
		token, bearer := parseBearer(v)
		if !bearer {
			if token != nil {
				t.Fatalf("non-Bearer value %q yielded token %q", v, token)
			}
			return
		}
		if len(token) > 0 && token[0] == ' ' {
			t.Fatalf("token %q starts with a space", token)
		}
		if !bytes.HasSuffix(v, token) {
			t.Fatalf("token %q is not a tail of %q", token, v)
		}
	})
}

// TestTokenDigest checks the comparison: the right token matches, and a
// different token of the same length, a prefix or an empty value does not.
// An unset digest matches nothing, not even an empty value.
func TestTokenDigest(t *testing.T) {
	d := newTokenDigest([]byte("0123456789abcdef0123456789abcdef"))
	if !d.matches([]byte("0123456789abcdef0123456789abcdef")) {
		t.Fatal("right token rejected")
	}
	for _, wrong := range []string{"0123456789abcdef0123456789abcdeX", "0123456789abcdef", "", "0123456789abcdef0123456789abcdef0"} {
		if d.matches([]byte(wrong)) {
			t.Fatalf("wrong token %q accepted", wrong)
		}
	}
	unset := newTokenDigest(nil)
	if unset.matches(nil) || unset.matches([]byte("x")) {
		t.Fatal("unset digest matched")
	}
}
