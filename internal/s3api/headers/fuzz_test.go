package headers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func FuzzParseRange(f *testing.F) {
	for _, s := range []string{
		"", "bytes=0-9", "bytes=90-", "bytes=-10", " bytes=1-2 ", "BYTES=1-2",
		"bytes=0-1,4-5", "bytes=0-1,-5", "items=0-1", "bytes=5-2", "bytes=-0",
		"bytes=9223372036854775808-", "bytes=-", "bytes=,,", "bytes=1--2",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRange(s)
		if err != nil {
			wantCode(t, err, s3err.InvalidRange)
			if r != nil {
				t.Fatal("range returned together with an error")
			}
			return
		}
		if r == nil {
			return
		}
		checkHTTPRange(t, *r)
	})
}

func FuzzParseCopySourceRange(f *testing.F) {
	for _, s := range []string{
		"", "bytes=0-9", "bytes=5-5", "bytes=10-5", "bytes=-5", "bytes=5-",
		"bytes=0-1,2-3", "items=0-1", "bytes=0-9223372036854775808", " bytes=1-2 ",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseCopySourceRange(s)
		if err != nil {
			wantCode(t, err, s3err.InvalidArgument)
			if r != nil {
				t.Fatal("range returned together with an error")
			}
			return
		}
		if r == nil {
			if strings.TrimSpace(s) != "" {
				t.Fatalf("nil range for non-blank input %q", s)
			}
			return
		}
		checkHTTPRange(t, *r)
		if r.Suffix != 0 || r.End < r.Start {
			t.Fatalf("copy source range must be closed, got %+v", *r)
		}
	})
}

func FuzzParseCopySource(f *testing.F) {
	for _, s := range []string{
		"bucket/key", "/bucket/key", "bucket/dir%2Fa%20b.txt", "bucket/key?versionId=v1",
		"bucket/", "bucket", "//key", "bucket/a%zz", "Bucket/key", "b%2Fx/key",
		"bucket/a?b", "bucket/key?versionId=", "bucket/%FF", "bucket/%E2%82%AC",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ref, err := ParseCopySource(s)
		if err != nil {
			switch s3err.CodeOf(err) {
			case s3err.InvalidArgument, s3err.InvalidBucketName, s3err.KeyTooLongError:
			default:
				t.Fatalf("unexpected code %s for %q: %v", s3err.CodeOf(err), s, err)
			}
			return
		}
		if err := object.ValidateBucketName(ref.Bucket); err != nil {
			t.Fatalf("bucket %q: %v", ref.Bucket, err)
		}
		if err := object.ValidateKey(ref.Key); err != nil {
			t.Fatalf("key %q: %v", ref.Key, err)
		}
		if len(ref.VersionID) > maxVersionIDLen {
			t.Fatalf("version id too long: %d bytes", len(ref.VersionID))
		}
	})
}

func FuzzParseTagging(f *testing.F) {
	for _, s := range []string{
		"", "k=v", "b=2&a=1", "a+b=c+d", "flag", "k=", "k=a=b", "%3D=v", "%D0%BA=%D0%B7",
		"a=1&a=2", "=v", "a=1&&b=2", "a=%ZZ", "%FF=v", "k=%00", "a=1&",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out, err := ParseTagging(s)
		if err != nil {
			wantCode(t, err, s3err.InvalidTag)
			if out != "" {
				t.Fatal("tag set returned together with an error")
			}
			return
		}
		if (s == "") != (out == "") {
			t.Fatalf("input %q gave output %q", s, out)
		}
		values, err := url.ParseQuery(out)
		if err != nil {
			t.Fatalf("canonical output %q does not parse: %v", out, err)
		}
		if len(values) > object.MaxTags {
			t.Fatalf("%d tags exceed the limit", len(values))
		}
		for k, v := range values {
			if k == "" || len(k) > maxTagKeyLen || len(v) != 1 || len(v[0]) > maxTagValueLen {
				t.Fatalf("tag %q=%q violates the bounds", k, v)
			}
		}
		if values.Encode() != out {
			t.Fatalf("output %q is not canonical", out)
		}
	})
}

func FuzzParseChecksumRequest(f *testing.F) {
	f.Add("AAAAAQ==", "CRC32", "")
	f.Add("", "SHA256", "")
	f.Add("", "", "x-amz-checksum-crc64nvme")
	f.Add("AAAAAAAAAAE=", "", "")
	f.Add("!!!!", "crc32", "x-amz-checksum-md5")
	f.Add("AAAAAQ==", "SHA1", "x-amz-checksum-sha1")
	f.Fuzz(func(t *testing.T, crc, algo, trailer string) {
		h := http.Header{}
		if crc != "" {
			h.Set("X-Amz-Checksum-Crc32", crc)
		}
		if algo != "" {
			h.Set("X-Amz-Checksum-Algorithm", algo)
		}
		if trailer != "" {
			h.Set("X-Amz-Trailer", trailer)
		}
		req, err := ParseChecksumRequest(h)
		if err != nil {
			wantCode(t, err, s3err.InvalidRequest)
			return
		}
		if req.TrailerDeclared && (trailer == "" || req.Algorithm == object.ChecksumNone) {
			t.Fatalf("trailer declared for %q without a checksum algorithm (%s)", trailer, req.Algorithm)
		}
		if req.Expected == nil {
			return
		}
		if req.Algorithm == object.ChecksumNone {
			t.Fatal("digest without algorithm")
		}
		if want := digestLen(req.Algorithm); len(req.Expected) != want {
			t.Fatalf("%s digest has %d bytes, want %d", req.Algorithm, len(req.Expected), want)
		}
	})
}

func FuzzParseContentMD5(f *testing.F) {
	for _, s := range []string{
		"", "1B2M2Y8AsgTpgAmY7PhCfg==", "1B2M2Y8AsgTpgAmY7PhCfg", "d41d8cd98f00b204e9800998ecf8427e",
		"!!!!", "AAAAAAAAAAAAAAAAAAAAAA==",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		h := http.Header{}
		h.Set("Content-MD5", s)
		sum, err := ParseContentMD5(h)
		if err != nil {
			wantCode(t, err, s3err.InvalidDigest)
			return
		}
		if len(sum) != 16 {
			t.Fatalf("digest has %d bytes", len(sum))
		}
	})
}

func FuzzParseConditions(f *testing.F) {
	f.Add(`"abc"`, "Sun, 06 Nov 1994 08:49:37 GMT")
	f.Add("*", "Sunday, 06-Nov-94 08:49:37 GMT")
	f.Add(`W/"x"`, "Sun Nov  6 08:49:37 1994")
	f.Add(`"a", "b"`, "yesterday")
	f.Add(`""`, "")
	f.Fuzz(func(t *testing.T, ifMatch, ifModified string) {
		h := http.Header{}
		h.Set("If-Match", ifMatch)
		h.Set("If-Modified-Since", ifModified)
		c, err := ParseConditions(h)
		if err != nil {
			wantCode(t, err, s3err.InvalidArgument)
			return
		}
		if c.IfMatch != "" {
			checkConditionETag(t, c.IfMatch)
		}
		if !c.IfModifiedSince.IsZero() && c.IfModifiedSince.Location() != time.UTC {
			t.Fatal("date is not UTC")
		}
	})
}

func FuzzParseUserMetadata(f *testing.F) {
	f.Add("owner", "bob")
	f.Add("Multi-Part", "")
	f.Add("", "x")
	f.Add("a", strings.Repeat("v", object.MaxUserMetadataSize))
	f.Fuzz(func(t *testing.T, name, value string) {
		h := http.Header{}
		h.Set("X-Amz-Meta-"+name, value)
		md, err := ParseUserMetadata(h)
		if err != nil {
			switch s3err.CodeOf(err) {
			case s3err.InvalidArgument, s3err.MetadataTooLarge:
			default:
				t.Fatalf("unexpected error for %q=%q: %v", name, value, err)
			}
			return
		}
		if md == nil {
			return
		}
		if err := object.ValidateUserMetadata(md); err != nil {
			t.Fatalf("metadata breaks the size limit: %v", err)
		}
		for k, v := range md {
			if k == "" || strings.ToLower(k) != k {
				t.Fatalf("metadata key %q is empty or not lower case", k)
			}
			if !utf8.ValidString(k) {
				t.Fatalf("metadata key %q is not UTF-8", k)
			}
			if containsControl(v) {
				t.Fatalf("metadata value for %q contains a control byte", k)
			}
		}
	})
}

// checkHTTPRange asserts the documented bounds of a parsed range.
func checkHTTPRange(t *testing.T, r object.HTTPRange) {
	t.Helper()
	if r.Start < 0 || r.Suffix < 0 {
		t.Fatalf("negative bound in %+v", r)
	}
	if r.Suffix == 0 && r.End != -1 && r.End < r.Start {
		t.Fatalf("last byte before first in %+v", r)
	}
}

// checkConditionETag asserts that an If-Match value is a bare ETag or "*".
func checkConditionETag(t *testing.T, etag string) {
	t.Helper()
	if !validConditionETag(etag) {
		t.Fatalf("invalid condition ETag %q", etag)
	}
}
