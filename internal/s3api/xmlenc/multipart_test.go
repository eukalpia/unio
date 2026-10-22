package xmlenc

import "testing"

func TestAppendInitiateMultipartUpload(t *testing.T) {
	in := InitiateMultipartUploadResult{Bucket: "b", Key: "dir/a&b.bin", UploadID: "u-1"}
	want := wantDecl + `<InitiateMultipartUploadResult ` + wantNS + `>` +
		`<Bucket>b</Bucket><Key>dir/a&amp;b.bin</Key><UploadId>u-1</UploadId>` +
		`</InitiateMultipartUploadResult>`
	checkAppend(t, want, func(dst []byte) []byte { return AppendInitiateMultipartUpload(dst, &in) })
}

func TestAppendCompleteMultipartUpload(t *testing.T) {
	tests := []struct {
		name string
		in   CompleteMultipartUploadResult
		want string
	}{
		{
			name: "checksums and checksum type",
			in: CompleteMultipartUploadResult{
				Location:     "https://h/b/k",
				Bucket:       "b",
				Key:          "k",
				ETag:         `"e-2"`,
				ChecksumType: "COMPOSITE",
				Checksums:    Checksums{CRC32: "AAAAAA==", SHA256: "c2hh"},
			},
			want: wantDecl + `<CompleteMultipartUploadResult ` + wantNS + `>` +
				`<Location>https://h/b/k</Location><Bucket>b</Bucket><Key>k</Key><ETag>&#34;e-2&#34;</ETag>` +
				`<ChecksumCRC32>AAAAAA==</ChecksumCRC32><ChecksumSHA256>c2hh</ChecksumSHA256>` +
				`<ChecksumType>COMPOSITE</ChecksumType>` +
				`</CompleteMultipartUploadResult>`,
		},
		{
			name: "no checksums omits their elements",
			in:   CompleteMultipartUploadResult{Location: "l", Bucket: "b", Key: "k", ETag: `"x"`},
			want: wantDecl + `<CompleteMultipartUploadResult ` + wantNS + `>` +
				`<Location>l</Location><Bucket>b</Bucket><Key>k</Key><ETag>&#34;x&#34;</ETag>` +
				`</CompleteMultipartUploadResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendCompleteMultipartUpload(dst, &tc.in) })
		})
	}
}

func TestAppendListParts(t *testing.T) {
	tests := []struct {
		name string
		in   ListPartsResult
		want string
	}{
		{
			name: "owner, parts and checksums",
			in: ListPartsResult{
				Bucket:               "b",
				Key:                  "big&file",
				UploadID:             "u1",
				StorageClass:         "STANDARD",
				ChecksumAlgorithm:    "SHA256",
				Owner:                &Owner{ID: "o1", DisplayName: "alice"},
				PartNumberMarker:     0,
				NextPartNumberMarker: 2,
				MaxParts:             2,
				IsTruncated:          true,
				Parts: []Part{
					{PartNumber: 1, LastModified: testTime, ETag: `"p1"`, Size: 5242880, Checksums: Checksums{SHA256: "cDE="}},
					{PartNumber: 2, LastModified: testTime, ETag: `"p2"`, Size: 100},
				},
			},
			want: wantDecl + `<ListPartsResult ` + wantNS + `>` +
				`<Bucket>b</Bucket><Key>big&amp;file</Key><UploadId>u1</UploadId>` +
				`<Initiator><ID>o1</ID><DisplayName>alice</DisplayName></Initiator>` +
				`<Owner><ID>o1</ID><DisplayName>alice</DisplayName></Owner>` +
				`<StorageClass>STANDARD</StorageClass><PartNumberMarker>0</PartNumberMarker>` +
				`<NextPartNumberMarker>2</NextPartNumberMarker><MaxParts>2</MaxParts><IsTruncated>true</IsTruncated>` +
				`<Part><PartNumber>1</PartNumber><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ETag>&#34;p1&#34;</ETag><Size>5242880</Size><ChecksumSHA256>cDE=</ChecksumSHA256></Part>` +
				`<Part><PartNumber>2</PartNumber><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ETag>&#34;p2&#34;</ETag><Size>100</Size></Part>` +
				`<ChecksumAlgorithm>SHA256</ChecksumAlgorithm>` +
				`</ListPartsResult>`,
		},
		{
			name: "no owner and no parts",
			in:   ListPartsResult{Bucket: "b", Key: "k", UploadID: "u", MaxParts: 1000},
			want: wantDecl + `<ListPartsResult ` + wantNS + `>` +
				`<Bucket>b</Bucket><Key>k</Key><UploadId>u</UploadId>` +
				`<StorageClass></StorageClass><PartNumberMarker>0</PartNumberMarker>` +
				`<NextPartNumberMarker>0</NextPartNumberMarker><MaxParts>1000</MaxParts><IsTruncated>false</IsTruncated>` +
				`<ChecksumAlgorithm></ChecksumAlgorithm>` +
				`</ListPartsResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendListParts(dst, &tc.in) })
		})
	}
}

func TestAppendListMultipartUploads(t *testing.T) {
	tests := []struct {
		name string
		in   ListMultipartUploadsResult
		want string
	}{
		{
			name: "url encoding with owner, uploads and common prefixes",
			in: ListMultipartUploadsResult{
				Bucket:             "b",
				KeyMarker:          "a b",
				UploadIDMarker:     "u0",
				NextKeyMarker:      "a c",
				NextUploadIDMarker: "u1",
				Prefix:             "a",
				Delimiter:          "/",
				EncodingType:       "url",
				MaxUploads:         2,
				IsTruncated:        true,
				Uploads: []Upload{
					{Key: "a/big file", UploadID: "u1", StorageClass: "STANDARD", Initiated: testTime,
						Owner: &Owner{ID: "o", DisplayName: "n"}},
					{Key: "a&b", UploadID: "u2", StorageClass: "STANDARD", Initiated: testTime},
				},
				CommonPrefixes: []string{"a/sub dir/"},
			},
			want: wantDecl + `<ListMultipartUploadsResult ` + wantNS + `>` +
				`<Bucket>b</Bucket><KeyMarker>a%20b</KeyMarker><UploadIdMarker>u0</UploadIdMarker>` +
				`<NextKeyMarker>a%20c</NextKeyMarker><Prefix>a</Prefix><Delimiter>/</Delimiter>` +
				`<NextUploadIdMarker>u1</NextUploadIdMarker><EncodingType>url</EncodingType>` +
				`<MaxUploads>2</MaxUploads><IsTruncated>true</IsTruncated>` +
				`<Upload><Key>a/big%20file</Key><UploadId>u1</UploadId>` +
				`<Initiator><ID>o</ID><DisplayName>n</DisplayName></Initiator>` +
				`<Owner><ID>o</ID><DisplayName>n</DisplayName></Owner>` +
				`<StorageClass>STANDARD</StorageClass><Initiated>2026-01-02T03:04:05.600Z</Initiated></Upload>` +
				`<Upload><Key>a%26b</Key><UploadId>u2</UploadId>` +
				`<StorageClass>STANDARD</StorageClass><Initiated>2026-01-02T03:04:05.600Z</Initiated></Upload>` +
				`<CommonPrefixes><Prefix>a/sub%20dir/</Prefix></CommonPrefixes>` +
				`</ListMultipartUploadsResult>`,
		},
		{
			name: "empty listing",
			in:   ListMultipartUploadsResult{Bucket: "b", MaxUploads: 1000},
			want: wantDecl + `<ListMultipartUploadsResult ` + wantNS + `>` +
				`<Bucket>b</Bucket><KeyMarker></KeyMarker><UploadIdMarker></UploadIdMarker>` +
				`<NextKeyMarker></NextKeyMarker><Prefix></Prefix><Delimiter></Delimiter>` +
				`<NextUploadIdMarker></NextUploadIdMarker><EncodingType></EncodingType>` +
				`<MaxUploads>1000</MaxUploads><IsTruncated>false</IsTruncated>` +
				`</ListMultipartUploadsResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendListMultipartUploads(dst, &tc.in) })
		})
	}
}
