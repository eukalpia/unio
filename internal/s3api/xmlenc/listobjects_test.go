package xmlenc

import "testing"

func TestAppendListObjectsV2(t *testing.T) {
	tests := []struct {
		name string
		in   ListObjectsV2Result
		want string
	}{
		{
			name: "minimal",
			in:   ListObjectsV2Result{Name: "bucket", MaxKeys: 1000},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>bucket</Name><Prefix></Prefix><KeyCount>0</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>` +
				`</ListBucketResult>`,
		},
		{
			name: "contents with and without owner, common prefixes",
			in: ListObjectsV2Result{
				Name:                  "photos",
				Prefix:                "2026/",
				Delimiter:             "/",
				StartAfter:            "2026/a",
				ContinuationToken:     "tok&1",
				NextContinuationToken: "tok&2",
				KeyCount:              2,
				MaxKeys:               2,
				IsTruncated:           true,
				Contents: []Content{
					{Key: "2026/a.jpg", ETag: `"abc"`, StorageClass: "STANDARD", LastModified: testTime, Size: 42,
						Owner: &Owner{ID: "id1", DisplayName: "alice"}, ChecksumAlgorithm: "CRC64NVME"},
					{Key: "2026/b", ETag: `"d"`, StorageClass: "STANDARD", LastModified: testTime},
				},
				CommonPrefixes: []string{"2026/x/"},
			},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>photos</Name><Prefix>2026/</Prefix><Delimiter>/</Delimiter><StartAfter>2026/a</StartAfter>` +
				`<ContinuationToken>tok&amp;1</ContinuationToken><NextContinuationToken>tok&amp;2</NextContinuationToken>` +
				`<KeyCount>2</KeyCount><MaxKeys>2</MaxKeys><IsTruncated>true</IsTruncated>` +
				`<Contents><Key>2026/a.jpg</Key><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ETag>&#34;abc&#34;</ETag><ChecksumAlgorithm>CRC64NVME</ChecksumAlgorithm><Size>42</Size>` +
				`<StorageClass>STANDARD</StorageClass><Owner><ID>id1</ID><DisplayName>alice</DisplayName></Owner></Contents>` +
				`<Contents><Key>2026/b</Key><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ETag>&#34;d&#34;</ETag><Size>0</Size><StorageClass>STANDARD</StorageClass></Contents>` +
				`<CommonPrefixes><Prefix>2026/x/</Prefix></CommonPrefixes>` +
				`</ListBucketResult>`,
		},
		{
			name: "encoding-type url percent-encodes key-like values only",
			in: ListObjectsV2Result{
				Name:                  "b&c",
				Prefix:                "my dir/",
				Delimiter:             "/",
				StartAfter:            "my dir/a&b",
				ContinuationToken:     "tok ?",
				NextContinuationToken: "next&",
				EncodingType:          "url",
				KeyCount:              1,
				MaxKeys:               1000,
				IsTruncated:           true,
				Contents: []Content{
					{Key: "my dir/a&<b>.txt", ETag: `"x"`, StorageClass: "STANDARD", LastModified: testTime},
				},
				CommonPrefixes: []string{"my dir/sub dir/"},
			},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>b&amp;c</Name><Prefix>my%20dir/</Prefix><Delimiter>/</Delimiter><StartAfter>my%20dir/a%26b</StartAfter>` +
				`<ContinuationToken>tok ?</ContinuationToken><NextContinuationToken>next&amp;</NextContinuationToken>` +
				`<EncodingType>url</EncodingType><KeyCount>1</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>true</IsTruncated>` +
				`<Contents><Key>my%20dir/a%26%3Cb%3E.txt</Key><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ETag>&#34;x&#34;</ETag><Size>0</Size><StorageClass>STANDARD</StorageClass></Contents>` +
				`<CommonPrefixes><Prefix>my%20dir/sub%20dir/</Prefix></CommonPrefixes>` +
				`</ListBucketResult>`,
		},
		{
			name: "special characters in key are escaped",
			in: ListObjectsV2Result{
				Name:     "b",
				MaxKeys:  1,
				Contents: []Content{{Key: "a&b<c>d\"e'f\r", LastModified: testTime}},
			},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>b</Name><Prefix></Prefix><KeyCount>0</KeyCount><MaxKeys>1</MaxKeys><IsTruncated>false</IsTruncated>` +
				`<Contents><Key>a&amp;b&lt;c&gt;d&#34;e&#39;f&#13;</Key><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ETag></ETag><Size>0</Size><StorageClass></StorageClass></Contents>` +
				`</ListBucketResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendListObjectsV2(dst, &tc.in) })
		})
	}
}

func TestAppendListObjectsV1(t *testing.T) {
	tests := []struct {
		name string
		in   ListObjectsV1Result
		want string
	}{
		{
			name: "minimal",
			in:   ListObjectsV1Result{Name: "bucket", MaxKeys: 100},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>bucket</Name><Prefix></Prefix><Marker></Marker><MaxKeys>100</MaxKeys><IsTruncated>false</IsTruncated>` +
				`</ListBucketResult>`,
		},
		{
			name: "full with owner and common prefixes",
			in: ListObjectsV1Result{
				Name:        "photos",
				Prefix:      "p/",
				Marker:      "p/a",
				Delimiter:   "/",
				NextMarker:  "p/b",
				MaxKeys:     1,
				IsTruncated: true,
				Contents: []Content{
					{Key: "p/a1", ETag: `"e"`, StorageClass: "STANDARD", LastModified: testTime, Size: 7,
						Owner: &Owner{ID: "o", DisplayName: "n"}},
				},
				CommonPrefixes: []string{"p/c/"},
			},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>photos</Name><Prefix>p/</Prefix><Marker>p/a</Marker><Delimiter>/</Delimiter><NextMarker>p/b</NextMarker>` +
				`<MaxKeys>1</MaxKeys><IsTruncated>true</IsTruncated>` +
				`<Contents><Key>p/a1</Key><LastModified>2026-01-02T03:04:05.600Z</LastModified><ETag>&#34;e&#34;</ETag>` +
				`<Size>7</Size><StorageClass>STANDARD</StorageClass><Owner><ID>o</ID><DisplayName>n</DisplayName></Owner></Contents>` +
				`<CommonPrefixes><Prefix>p/c/</Prefix></CommonPrefixes>` +
				`</ListBucketResult>`,
		},
		{
			name: "encoding-type url percent-encodes marker and keys",
			in: ListObjectsV1Result{
				Name:         "b",
				Prefix:       "a b/",
				Marker:       "a b/&",
				Delimiter:    "/",
				NextMarker:   "a b/~",
				EncodingType: "url",
				MaxKeys:      5,
				IsTruncated:  true,
				Contents: []Content{
					{Key: "a b/é.txt", ETag: `"1"`, StorageClass: "STANDARD", LastModified: testTime, Size: 2},
				},
				CommonPrefixes: []string{"a b/c/"},
			},
			want: wantDecl + `<ListBucketResult ` + wantNS + `>` +
				`<Name>b</Name><Prefix>a%20b/</Prefix><Marker>a%20b/%26</Marker><Delimiter>/</Delimiter><NextMarker>a%20b/~</NextMarker>` +
				`<EncodingType>url</EncodingType><MaxKeys>5</MaxKeys><IsTruncated>true</IsTruncated>` +
				`<Contents><Key>a%20b/%C3%A9.txt</Key><LastModified>2026-01-02T03:04:05.600Z</LastModified><ETag>&#34;1&#34;</ETag>` +
				`<Size>2</Size><StorageClass>STANDARD</StorageClass></Contents>` +
				`<CommonPrefixes><Prefix>a%20b/c/</Prefix></CommonPrefixes>` +
				`</ListBucketResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendListObjectsV1(dst, &tc.in) })
		})
	}
}
