package xmldec

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

func TestReadBody(t *testing.T) {
	got, err := ReadBody(strings.NewReader("<a/>"))
	if err != nil || string(got) != "<a/>" {
		t.Fatalf("ReadBody = %q, %v", got, err)
	}
	big := bytes.Repeat([]byte("x"), object.MaxXMLBodySize+1)
	if _, err := ReadBody(bytes.NewReader(big)); !s3err.Is(err, s3err.MaxMessageLengthExceeded) {
		t.Fatalf("oversized body: got %v", err)
	}
	exact := bytes.Repeat([]byte(" "), object.MaxXMLBodySize)
	if got, err := ReadBody(bytes.NewReader(exact)); err != nil || len(got) != object.MaxXMLBodySize {
		t.Fatalf("body at the limit: len %d, err %v", len(got), err)
	}
}

func TestParseCompleteMultipartUpload(t *testing.T) {
	const ok = `<?xml version="1.0" encoding="UTF-8"?>
<CompleteMultipartUpload xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Part><PartNumber>1</PartNumber><ETag>"abc"</ETag></Part>
  <Part><PartNumber>2</PartNumber><ETag>def</ETag><ChecksumCRC32>AAAAAQ==</ChecksumCRC32></Part>
</CompleteMultipartUpload>`
	parts, err := ParseCompleteMultipartUpload([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || parts[0].PartNumber != 1 || parts[0].ETag != "abc" ||
		parts[1].PartNumber != 2 || parts[1].ETag != "def" ||
		parts[1].Checksum.Algorithm != object.ChecksumCRC32 || len(parts[1].Checksum.Value) != 4 {
		t.Fatalf("parts = %+v", parts)
	}

	cases := []struct {
		name string
		body string
		code s3err.Code
	}{
		{"empty", "", s3err.MalformedXML},
		{"wrong root", `<Delete><Object/></Delete>`, s3err.MalformedXML},
		{"no parts", `<CompleteMultipartUpload></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"unknown element", `<CompleteMultipartUpload><Foo/></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"part number not integer", `<CompleteMultipartUpload><Part><PartNumber>x</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`, s3err.InvalidArgument},
		{"part number zero", `<CompleteMultipartUpload><Part><PartNumber>0</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`, s3err.InvalidArgument},
		{"part number too big", `<CompleteMultipartUpload><Part><PartNumber>10001</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`, s3err.InvalidArgument},
		{"descending order", `<CompleteMultipartUpload><Part><PartNumber>2</PartNumber><ETag>"a"</ETag></Part><Part><PartNumber>1</PartNumber><ETag>"b"</ETag></Part></CompleteMultipartUpload>`, s3err.InvalidPartOrder},
		{"duplicate part number", `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag></Part><Part><PartNumber>1</PartNumber><ETag>"b"</ETag></Part></CompleteMultipartUpload>`, s3err.InvalidPartOrder},
		{"bad checksum base64", `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag><ChecksumCRC32>!!</ChecksumCRC32></Part></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"checksum wrong length", `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag><ChecksumCRC32>AAAAAAAAAAA=</ChecksumCRC32></Part></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"two checksums in one part", `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag><ChecksumCRC32>AAAAAQ==</ChecksumCRC32><ChecksumSHA1>AAAAAAAAAAAAAAAAAAAAAAAAAAA=</ChecksumSHA1></Part></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"doctype rejected", `<!DOCTYPE x [<!ENTITY e "y">]><CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"trailing garbage", `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload><x/>`, s3err.MalformedXML},
		{"text where element expected", `<CompleteMultipartUpload>junk<Part><PartNumber>1</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`, s3err.MalformedXML},
		{"truncated", `<CompleteMultipartUpload><Part><PartNumber>1`, s3err.MalformedXML},
		{"nested element in text", `<CompleteMultipartUpload><Part><PartNumber><b>1</b></PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`, s3err.MalformedXML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseCompleteMultipartUpload([]byte(tc.body))
			if !s3err.Is(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestParseCompleteRejectsTooManyParts(t *testing.T) {
	var b strings.Builder
	b.WriteString("<CompleteMultipartUpload>")
	for i := 1; i <= object.MaxPartNumber+1; i++ {
		b.WriteString(`<Part><PartNumber>`)
		b.WriteString(itoa(i))
		b.WriteString(`</PartNumber><ETag>"e"</ETag></Part>`)
	}
	b.WriteString("</CompleteMultipartUpload>")
	if _, err := ParseCompleteMultipartUpload([]byte(b.String())); !s3err.Is(err, s3err.MalformedXML) {
		t.Fatalf("got %v, want MalformedXML", err)
	}
}

func TestParseDeleteObjects(t *testing.T) {
	const body = `<Delete xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Quiet>true</Quiet>
  <Object><Key> a &amp; b.txt </Key></Object>
  <Object><Key>c</Key><VersionId>v1</VersionId></Object>
</Delete>`
	req, err := ParseDeleteObjects([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if !req.Quiet || len(req.Objects) != 2 {
		t.Fatalf("req = %+v", req)
	}
	if req.Objects[0].Key != " a & b.txt " || req.Objects[1].Key != "c" || req.Objects[1].VersionID != "v1" {
		t.Fatalf("objects = %+v (keys must not be trimmed)", req.Objects)
	}

	cases := []struct {
		name string
		body string
		code s3err.Code
	}{
		{"empty", "", s3err.MalformedXML},
		{"no objects", `<Delete><Quiet>false</Quiet></Delete>`, s3err.MalformedXML},
		{"unknown element", `<Delete><Object><Key>a</Key><Bogus/></Object></Delete>`, s3err.MalformedXML},
		{"quiet not boolean", `<Delete><Quiet>yes</Quiet><Object><Key>a</Key></Object></Delete>`, s3err.MalformedXML},
		{"missing key", `<Delete><Object><VersionId>v</VersionId></Object></Delete>`, s3err.MalformedXML},
		{"wrong root", `<CompleteMultipartUpload/>`, s3err.MalformedXML},
		{"entity expansion", `<!DOCTYPE d [<!ENTITY a "aaaa">]><Delete><Object><Key>&a;</Key></Object></Delete>`, s3err.MalformedXML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseDeleteObjects([]byte(tc.body)); !s3err.Is(err, tc.code) {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestParseDeleteObjectsLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("<Delete>")
	for i := 0; i < object.MaxDeleteObjects+1; i++ {
		b.WriteString("<Object><Key>k</Key></Object>")
	}
	b.WriteString("</Delete>")
	if _, err := ParseDeleteObjects([]byte(b.String())); !s3err.Is(err, s3err.MalformedXML) {
		t.Fatalf("got %v, want MalformedXML", err)
	}
}

func TestParseCreateBucketConfiguration(t *testing.T) {
	cases := []struct {
		name, body, want string
		code             s3err.Code
	}{
		{"empty body means default", "", "", ""},
		{"location", `<CreateBucketConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><LocationConstraint>eu-west-1</LocationConstraint></CreateBucketConfiguration>`, "eu-west-1", ""},
		{"location with spaces", `<CreateBucketConfiguration><LocationConstraint> us-east-1 </LocationConstraint></CreateBucketConfiguration>`, "us-east-1", ""},
		{"empty location", `<CreateBucketConfiguration><LocationConstraint/></CreateBucketConfiguration>`, "", ""},
		{"unknown child", `<CreateBucketConfiguration><Foo>x</Foo></CreateBucketConfiguration>`, "", s3err.MalformedXML},
		{"wrong root", `<Delete/>`, "", s3err.MalformedXML},
		{"whitespace only", "  \n ", "", s3err.MalformedXML},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCreateBucketConfiguration([]byte(tc.body))
			if tc.code != "" {
				if !s3err.Is(err, tc.code) {
					t.Fatalf("err = %v, want %s", err, tc.code)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func itoa(i int) string {
	var buf [12]byte
	n := len(buf)
	for {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
		if i == 0 {
			break
		}
	}
	return string(buf[n:])
}
