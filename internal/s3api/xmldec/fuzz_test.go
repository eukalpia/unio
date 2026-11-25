package xmldec

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// checkCode fails the fuzz run when a parser returns an unclassified error,
// because every client-input failure must map to a specific S3 code.
func checkCode(t *testing.T, err error) {
	t.Helper()
	if err != nil && s3err.CodeOf(err) == s3err.InternalError {
		t.Fatalf("client input produced an internal error: %v", err)
	}
}

func FuzzParseCompleteMultipartUpload(f *testing.F) {
	f.Add([]byte(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"a"</ETag></Part></CompleteMultipartUpload>`))
	f.Add([]byte(`<CompleteMultipartUpload><Part><PartNumber>2</PartNumber><ETag>x</ETag><ChecksumCRC32>AAAAAQ==</ChecksumCRC32></Part></CompleteMultipartUpload>`))
	f.Add([]byte(`<!DOCTYPE x><CompleteMultipartUpload/>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		parts, err := ParseCompleteMultipartUpload(data)
		checkCode(t, err)
		if err != nil {
			return
		}
		if len(parts) == 0 {
			t.Fatal("success with no parts")
		}
		for i, p := range parts {
			if p.PartNumber < 1 || p.PartNumber > object.MaxPartNumber {
				t.Fatalf("part %d: number %d out of range", i, p.PartNumber)
			}
			if i > 0 && p.PartNumber <= parts[i-1].PartNumber {
				t.Fatal("part numbers not strictly ascending")
			}
			if !p.Checksum.IsZero() && len(p.Checksum.Value) != digestLength[p.Checksum.Algorithm] {
				t.Fatal("checksum length does not match algorithm")
			}
		}
	})
}

func FuzzParseDeleteObjects(f *testing.F) {
	f.Add([]byte(`<Delete><Quiet>true</Quiet><Object><Key>a</Key></Object></Delete>`))
	f.Add([]byte(`<Delete><Object><Key>a</Key><VersionId>v</VersionId></Object></Delete>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		req, err := ParseDeleteObjects(data)
		checkCode(t, err)
		if err == nil && (len(req.Objects) == 0 || len(req.Objects) > object.MaxDeleteObjects) {
			t.Fatalf("success with %d objects", len(req.Objects))
		}
	})
}

func FuzzParseCreateBucketConfiguration(f *testing.F) {
	f.Add([]byte(`<CreateBucketConfiguration><LocationConstraint>eu-west-1</LocationConstraint></CreateBucketConfiguration>`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, data []byte) {
		loc, err := ParseCreateBucketConfiguration(data)
		checkCode(t, err)
		if err == nil && strings.TrimSpace(loc) != loc {
			t.Fatal("location not trimmed")
		}
	})
}

func FuzzParseTagging(f *testing.F) {
	f.Add([]byte(`<Tagging><TagSet><Tag><Key>a</Key><Value>b</Value></Tag></TagSet></Tagging>`))
	f.Add([]byte(`<Tagging><TagSet/></Tagging>`))
	f.Add([]byte(`<!DOCTYPE x><Tagging/>`))
	f.Fuzz(func(t *testing.T, data []byte) {
		tags, err := ParseTagging(data)
		checkCode(t, err)
		if err != nil {
			if tags != nil {
				t.Fatal("tag set returned together with an error")
			}
			return
		}
		if len(tags) > object.MaxTags {
			t.Fatalf("%d tags exceed the limit", len(tags))
		}
	})
}
