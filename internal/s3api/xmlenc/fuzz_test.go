package xmlenc

import (
	"encoding/xml"
	"io"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

// decodeXMLText parses doc and returns the character data found directly
// inside the root element.
func decodeXMLText(t *testing.T, doc string) string {
	t.Helper()
	dec := xml.NewDecoder(strings.NewReader(doc))
	var sb strings.Builder
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return sb.String()
		}
		if err != nil {
			t.Fatalf("decoding %q: %v", doc, err)
		}
		switch v := tok.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 1 {
				sb.Write(v)
			}
		}
	}
}

// xmlRuneOK reports whether r may appear in an XML 1.0 document.
func xmlRuneOK(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// wantText is the text a conforming parser must recover from AppendEscape's
// output: every invalid byte and every XML-invalid code point becomes U+FFFD.
func wantText(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if (r == utf8.RuneError && n == 1) || !xmlRuneOK(r) {
			sb.WriteRune(utf8.RuneError)
			continue
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// isUpperHexDigit reports whether c is 0-9 or A-F.
func isUpperHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')
}

// checkPercentSyntax fails the test unless b contains only unreserved bytes,
// '/', and %XX escapes with uppercase hex digits.
func checkPercentSyntax(t *testing.T, b []byte) {
	t.Helper()
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
			c == '-', c == '.', c == '_', c == '~', c == '/':
		case c == '%':
			if i+2 >= len(b) || !isUpperHexDigit(b[i+1]) || !isUpperHexDigit(b[i+2]) {
				t.Fatalf("malformed escape at offset %d in %q", i, b)
			}
			i += 2
		default:
			t.Fatalf("unexpected byte %#x at offset %d in %q", c, i, b)
		}
	}
}

// checkDecodedKey verifies a key read back from the XML text against the
// input key, using the encoding-type rules of the containing result.
func checkDecodedKey(t *testing.T, decoded, input string, urlMode bool) {
	t.Helper()
	if urlMode {
		back, err := url.PathUnescape(decoded)
		if err != nil || back != input {
			t.Fatalf("url-encoded key %q unescapes to %q (err %v), want %q", decoded, back, err, input)
		}
		return
	}
	if want := wantText(input); decoded != want {
		t.Fatalf("key decoded as %q, want %q (input %q)", decoded, want, input)
	}
}

func FuzzAppendEscape(f *testing.F) {
	for _, s := range []string{
		"", "&<>\"'", "\r", "\x00", "\xff", "😀", "a\tb\nc", "]]>",
		"\xed\xa0\x80", "￾", "\xe2\x82", "\x7f\u0085",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := AppendEscape(nil, s)
		got := decodeXMLText(t, "<v>"+string(out)+"</v>")
		if want := wantText(s); got != want {
			t.Fatalf("decoded %q, want %q (input %q)", got, want, s)
		}
	})
}

func FuzzAppendPercent(f *testing.F) {
	for _, s := range []string{"", "a b", "a+b", "%41", "/~-._", "é", "😀", "\xff", "&<>\"'", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := AppendPercent(nil, s)
		checkPercentSyntax(t, out)
		back, err := url.PathUnescape(string(out))
		if err != nil {
			t.Fatalf("PathUnescape(%q): %v", out, err)
		}
		if back != s {
			t.Fatalf("PathUnescape(%q) = %q, want %q", out, back, s)
		}
	})
}

func FuzzAppendListObjectsV2(f *testing.F) {
	f.Add("a&b", "c<d>", "p/", false)
	f.Add("\xff\x00", "😀\r", "", true)
	f.Add("]]>", "\"'", "x y/", false)
	f.Add("\xed\xa0\x80", "￾", "\U0010FFFF", true)
	f.Fuzz(func(t *testing.T, k1, k2, prefix string, urlMode bool) {
		r := ListObjectsV2Result{
			Name:        "bucket",
			Prefix:      prefix,
			KeyCount:    2,
			MaxKeys:     1000,
			IsTruncated: true,
			Contents: []Content{
				{Key: k1, ETag: `"e"`, StorageClass: "STANDARD", LastModified: testTime},
				{Key: k2, ETag: `"e"`, StorageClass: "STANDARD", LastModified: testTime},
			},
			CommonPrefixes: []string{prefix},
		}
		if urlMode {
			r.EncodingType = "url"
		}
		var got rtListV2
		if err := xml.Unmarshal(AppendListObjectsV2(nil, &r), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(got.Contents) != 2 || len(got.CommonPrefixes) != 1 {
			t.Fatalf("got %d contents and %d common prefixes, want 2 and 1", len(got.Contents), len(got.CommonPrefixes))
		}
		checkDecodedKey(t, got.Contents[0].Key, k1, urlMode)
		checkDecodedKey(t, got.Contents[1].Key, k2, urlMode)
		checkDecodedKey(t, got.CommonPrefixes[0].Prefix, prefix, urlMode)
	})
}
