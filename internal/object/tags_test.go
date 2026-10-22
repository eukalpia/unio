package object

import (
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/eukalpia/unio/internal/s3err"
)

// canonicalTags decodes an encoded tag set and returns its stored form.
func canonicalTags(in string) (string, error) {
	set, err := DecodeTags(in)
	if err != nil {
		return "", err
	}
	return EncodeTags(set), nil
}

func TestDecodeTagsCanonical(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
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
		{name: "ten tags", in: tagList(MaxTags), want: tagList(MaxTags)},
		{name: "key at the length limit", in: strings.Repeat("k", MaxTagKeyLength) + "=v", want: strings.Repeat("k", MaxTagKeyLength) + "=v"},
		{name: "value at the length limit", in: "k=" + strings.Repeat("v", MaxTagValueLength), want: "k=" + strings.Repeat("v", MaxTagValueLength)},
		{name: "key of multi-byte characters at the limit", in: strings.Repeat("é", MaxTagKeyLength) + "=v", want: url.QueryEscape(strings.Repeat("é", MaxTagKeyLength)) + "=v"},
		{name: "value of multi-byte characters at the limit", in: "k=" + strings.Repeat("€", MaxTagValueLength), want: "k=" + url.QueryEscape(strings.Repeat("€", MaxTagValueLength))},
		{name: "key that only looks like the prefix", in: "aw:s=v", want: "aw%3As=v"},
		{name: "prefix not at the start", in: "x-aws:k=v", want: "x-aws%3Ak=v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalTags(tc.in)
			if err != nil {
				t.Fatalf("DecodeTags(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("canonical form of %q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecodeTagsRejects(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{name: "malformed escape", in: "a=%ZZ"},
		{name: "truncated escape in key", in: "a%2=1"},
		{name: "duplicate key", in: "a=1&a=2"},
		{name: "duplicate key after decoding", in: "a=1&%61=2"},
		{name: "empty key", in: "=v"},
		{name: "empty pair in middle", in: "a=1&&b=2"},
		{name: "trailing ampersand", in: "a=1&"},
		{name: "eleven tags", in: tagList(MaxTags + 1)},
		{name: "key over the limit", in: strings.Repeat("k", MaxTagKeyLength+1) + "=v"},
		{name: "key of multi-byte characters over the limit", in: strings.Repeat("é", MaxTagKeyLength+1) + "=v"},
		{name: "value over the limit", in: "k=" + strings.Repeat("v", MaxTagValueLength+1)},
		{name: "value of multi-byte characters over the limit", in: "k=" + strings.Repeat("€", MaxTagValueLength+1)},
		{name: "invalid utf-8 key", in: "%FF=v"},
		{name: "invalid utf-8 value", in: "k=%C3"},
		{name: "control byte in value", in: "k=%00"},
		{name: "tab in key", in: "a%09b=v"},
		{name: "reserved prefix", in: "aws:created-by=me"},
		{name: "reserved prefix in upper case", in: "AWS:created-by=me"},
		{name: "reserved prefix in mixed case", in: "Aws:x=y"},
		{name: "reserved prefix alone", in: "aws:=v"},
		{name: "too many pairs in raw input", in: strings.Repeat("a=b&", 3000)},
		{name: "longer than the encoded limit", in: "k=" + strings.Repeat("%FF", MaxEncodedTagsLength)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set, err := DecodeTags(tc.in)
			if s3err.CodeOf(err) != s3err.InvalidTag {
				t.Fatalf("DecodeTags(%q) = %v, %v; want InvalidTag", tc.in, set, err)
			}
			if set != nil {
				t.Fatal("tag set returned together with an error")
			}
		})
	}
}

func TestValidateTags(t *testing.T) {
	ok := func(name string, set TagSet) {
		t.Helper()
		if err := ValidateTags(set); err != nil {
			t.Fatalf("%s: ValidateTags: %v", name, err)
		}
	}
	bad := func(name string, set TagSet) {
		t.Helper()
		if s3err.CodeOf(ValidateTags(set)) != s3err.InvalidTag {
			t.Fatalf("%s: ValidateTags accepted an invalid set", name)
		}
	}
	ok("empty set", nil)
	ok("empty value", TagSet{{Key: "k", Value: ""}})
	ok("ten tags", tagSetOf(MaxTags))
	ok("key of 128 characters", TagSet{{Key: strings.Repeat("a", MaxTagKeyLength), Value: "v"}})
	ok("key of 128 characters of 4 bytes each", TagSet{{Key: strings.Repeat("\U0001F600", MaxTagKeyLength), Value: "v"}})
	bad("eleven tags", tagSetOf(MaxTags+1))
	bad("empty key", TagSet{{Key: "", Value: "v"}})
	bad("key of 129 characters", TagSet{{Key: strings.Repeat("a", MaxTagKeyLength+1), Value: "v"}})
	bad("key of 129 characters of 4 bytes each", TagSet{{Key: strings.Repeat("\U0001F600", MaxTagKeyLength+1), Value: "v"}})
	bad("value of 257 characters", TagSet{{Key: "k", Value: strings.Repeat("a", MaxTagValueLength+1)}})
	bad("duplicate keys", TagSet{{Key: "a", Value: "1"}, {Key: "a", Value: "2"}})
	bad("reserved prefix", TagSet{{Key: "aws:x", Value: "1"}})
	bad("reserved prefix in upper case", TagSet{{Key: "AWS:x", Value: "1"}})
	bad("invalid utf-8 key", TagSet{{Key: "\xff", Value: "1"}})
	bad("control character in value", TagSet{{Key: "k", Value: "a\nb"}})
	bad("delete character in key", TagSet{{Key: "a\x7f", Value: "1"}})
}

// TestTagLengthCountsCharacters pins the rule that key and value lengths count
// Unicode characters, not bytes: a 4-byte character is one character.
func TestTagLengthCountsCharacters(t *testing.T) {
	key := strings.Repeat("\U0001F600", MaxTagKeyLength)
	if len(key) <= MaxTagKeyLength || utf8.RuneCountInString(key) != MaxTagKeyLength {
		t.Fatal("test key is not a run of multi-byte characters at the limit")
	}
	if err := ValidateTags(TagSet{{Key: key, Value: "v"}}); err != nil {
		t.Fatalf("a key of %d characters was rejected: %v", MaxTagKeyLength, err)
	}
}

func TestEncodeTagsRoundTrip(t *testing.T) {
	in := TagSet{{Key: "team", Value: "storage & backup"}, {Key: "env", Value: "it=1"}, {Key: "é", Value: ""}}
	if err := ValidateTags(in); err != nil {
		t.Fatal(err)
	}
	encoded := EncodeTags(in)
	if encoded != "env=it%3D1&team=storage+%26+backup&%C3%A9=" {
		t.Fatalf("EncodeTags = %q", encoded)
	}
	back, err := DecodeTags(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got := EncodeTags(back); got != encoded {
		t.Fatalf("round trip = %q, want %q", got, encoded)
	}
	if len(back) != len(in) {
		t.Fatalf("round trip has %d tags, want %d", len(back), len(in))
	}
	if EncodeTags(nil) != "" {
		t.Fatal("empty set does not encode as the empty string")
	}
}

// FuzzDecodeTags checks the parser of untrusted tag sets: errors are InvalidTag
// and carry no tag set, and every accepted set is valid and re-encodes to a
// form that decodes to the same set.
func FuzzDecodeTags(f *testing.F) {
	for _, s := range []string{
		"", "k=v", "b=2&a=1", "a+b=c+d", "flag", "k=", "k=a=b", "%3D=v", "%D0%BA=%D0%B7",
		"a=1&a=2", "=v", "a=1&&b=2", "a=%ZZ", "%FF=v", "k=%00", "a=1&", "aws:x=1", "AWS%3Ax=1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		set, err := DecodeTags(s)
		if err != nil {
			if s3err.CodeOf(err) != s3err.InvalidTag {
				t.Fatalf("error %v is not InvalidTag", err)
			}
			if set != nil {
				t.Fatal("tag set returned together with an error")
			}
			return
		}
		if err := ValidateTags(set); err != nil {
			t.Fatalf("accepted set fails validation: %v", err)
		}
		encoded := EncodeTags(set)
		again, err := DecodeTags(encoded)
		if err != nil {
			t.Fatalf("canonical form %q does not decode: %v", encoded, err)
		}
		if EncodeTags(again) != encoded {
			t.Fatalf("canonical form %q is not stable", encoded)
		}
	})
}

// tagList returns n tags k00=v, k01=v, ... joined with '&'.
func tagList(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "k" + string(rune('0'+i/10)) + string(rune('0'+i%10)) + "=v"
	}
	return strings.Join(parts, "&")
}

// tagSetOf returns a valid set of n tags with distinct keys.
func tagSetOf(n int) TagSet {
	set := make(TagSet, n)
	for i := range set {
		set[i] = Tag{Key: "k" + string(rune('0'+i/10)) + string(rune('0'+i%10)), Value: "v"}
	}
	return set
}
