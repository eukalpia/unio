package xmlenc

import (
	"testing"
	"time"
)

// TestAppendListObjectVersions checks the element shape of a ListObjectVersions
// response: the null version of each object, the optional markers and the
// url encoding of key-like values.
func TestAppendListObjectVersions(t *testing.T) {
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name string
		in   ListVersionsResult
		want string
	}{
		{
			name: "empty bucket",
			in:   ListVersionsResult{Name: "bucket", MaxKeys: 1000},
			want: wantDecl + `<ListVersionsResult ` + wantNS + `>` +
				`<Name>bucket</Name><Prefix></Prefix><KeyMarker></KeyMarker><VersionIdMarker></VersionIdMarker>` +
				`<MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated></ListVersionsResult>`,
		},
		{
			name: "null version with owner, truncated",
			in: ListVersionsResult{
				Name:                "b",
				Prefix:              "p/",
				KeyMarker:           "p/a",
				VersionIDMarker:     "null",
				NextKeyMarker:       "p/b",
				NextVersionIDMarker: "null",
				MaxKeys:             1,
				IsTruncated:         true,
				Versions: []Version{{
					Key:          "p/b&1",
					VersionID:    "null",
					IsLatest:     true,
					LastModified: when,
					ETag:         `"e"`,
					Size:         3,
					StorageClass: "STANDARD",
					Owner:        &Owner{ID: "u", DisplayName: "u"},
				}},
			},
			want: wantDecl + `<ListVersionsResult ` + wantNS + `>` +
				`<Name>b</Name><Prefix>p/</Prefix><KeyMarker>p/a</KeyMarker><VersionIdMarker>null</VersionIdMarker>` +
				`<NextKeyMarker>p/b</NextKeyMarker><NextVersionIdMarker>null</NextVersionIdMarker>` +
				`<MaxKeys>1</MaxKeys><IsTruncated>true</IsTruncated>` +
				`<Version><Key>p/b&amp;1</Key><VersionId>null</VersionId><IsLatest>true</IsLatest>` +
				`<LastModified>2026-01-02T03:04:05.000Z</LastModified><ETag>&#34;e&#34;</ETag><Size>3</Size>` +
				`<StorageClass>STANDARD</StorageClass><Owner><ID>u</ID><DisplayName>u</DisplayName></Owner></Version>` +
				`</ListVersionsResult>`,
		},
		{
			name: "url encoding applies to the prefix and the common prefixes",
			in: ListVersionsResult{
				Name:           "b",
				Prefix:         "a b/",
				Delimiter:      "/",
				EncodingType:   "url",
				MaxKeys:        10,
				CommonPrefixes: []string{"a b/c/"},
			},
			want: wantDecl + `<ListVersionsResult ` + wantNS + `>` +
				`<Name>b</Name><Prefix>a%20b/</Prefix><KeyMarker></KeyMarker><VersionIdMarker></VersionIdMarker>` +
				`<Delimiter>/</Delimiter><EncodingType>url</EncodingType>` +
				`<MaxKeys>10</MaxKeys><IsTruncated>false</IsTruncated>` +
				`<CommonPrefixes><Prefix>a%20b/c/</Prefix></CommonPrefixes>` +
				`</ListVersionsResult>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(AppendListObjectVersions(nil, &tt.in))
			if got != tt.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}
