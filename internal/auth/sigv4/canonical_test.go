package sigv4

import (
	"net/http"
	"testing"
)

func TestCanonicalURI(t *testing.T) {
	cases := []struct {
		name    string
		rawPath string
		want    string
		wantErr bool
	}{
		{"empty path is root", "", "/", false},
		{"root", "/", "/", false},
		{"plain object", "/test.txt", "/test.txt", false},
		{"already encoded space", "/a%20b/c", "/a%20b/c", false},
		{"raw space is encoded", "/a b", "/a%20b", false},
		{"tilde is unreserved", "/~tilde%7E", "/~tilde~", false},
		{"dollar is escaped once", "/test$file.text", "/test%24file.text", false},
		{"plus is escaped", "/plus+sign", "/plus%2Bsign", false},
		{"utf8 is escaped", "/%E2%82%AC", "/%E2%82%AC", false},
		{"encoded slash is a separator", "/a%2Fb", "/a/b", false},
		{"lowercase hex is normalised", "/a%2fb%c3%a9", "/a/b%C3%A9", false},
		{"bad escape", "/%zz", "", true},
		{"truncated escape", "/%2", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := canonicalURI(c.rawPath)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestCanonicalQuery(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		exclude string
		want    string
		wantErr bool
	}{
		{"empty", "", "", "", false},
		{"sorted by key", "prefix=J&max-keys=2", "", "max-keys=2&prefix=J", false},
		{"sorted by value for equal keys", "a=1&b=2&a=0", "", "a=0&a=1&b=2", false},
		{"space and plus", "a=b%20c+d", "", "a=b%20c%2Bd", false},
		{"key without value", "key", "", "key=", false},
		{"slash is escaped in query", "a/b=c", "", "a%2Fb=c", false},
		{"unreserved stays", "~=~", "", "~=~", false},
		{"excluded signature", "X-Amz-Signature=abc&X-Amz-Date=1", "X-Amz-Signature", "X-Amz-Date=1", false},
		{"encoded key matches exclusion", "X%2DAmz%2DSignature=abc", "X-Amz-Signature", "", false},
		{"bad escape", "a=%G0", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			params, err := parseQuery(c.raw)
			if err == nil {
				got := canonicalQuery(params, c.exclude)
				if got != c.want {
					t.Fatalf("got %q want %q", got, c.want)
				}
			}
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestCanonicalRequestAWSExample(t *testing.T) {
	// The GET Object example from the S3 SigV4 documentation.
	h := http.Header{}
	h.Set("Range", "bytes=0-9")
	h.Set("X-Amz-Content-Sha256", emptyPayloadSHA256)
	h.Set("X-Amz-Date", "20130524T000000Z")
	got, err := canonicalRequest(canonicalSpec{
		Method:        "GET",
		RawPath:       "/test.txt",
		Host:          "examplebucket.s3.amazonaws.com",
		Header:        h,
		SignedHeaders: []string{"host", "range", "x-amz-content-sha256", "x-amz-date"},
		PayloadHash:   emptyPayloadSHA256,
		ContentLength: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "GET\n/test.txt\n\n" +
		"host:examplebucket.s3.amazonaws.com\n" +
		"range:bytes=0-9\n" +
		"x-amz-content-sha256:" + emptyPayloadSHA256 + "\n" +
		"x-amz-date:20130524T000000Z\n\n" +
		"host;range;x-amz-content-sha256;x-amz-date\n" +
		emptyPayloadSHA256
	if got != want {
		t.Fatalf("canonical request mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestNormalizeValue(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"plain":           "plain",
		"  a   b  ":       "a b",
		"x  y  z":         "x y z",
		"tab\there":       "tab\there",
		"   spaced out  ": "spaced out",
	}
	for in, want := range cases {
		if got := normalizeValue(in); got != want {
			t.Fatalf("normalizeValue(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalHeadersMissingSignedHeader(t *testing.T) {
	_, err := canonicalRequest(canonicalSpec{
		Method:        "GET",
		RawPath:       "/",
		Host:          "h",
		Header:        http.Header{},
		SignedHeaders: []string{"host", "x-amz-date"},
		PayloadHash:   emptyPayloadSHA256,
		ContentLength: -1,
	})
	if err == nil {
		t.Fatal("expected error for a signed header that is absent")
	}
}
