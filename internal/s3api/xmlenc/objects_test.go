package xmlenc

import "testing"

func TestAppendCopyObject(t *testing.T) {
	tests := []struct {
		name string
		in   CopyObjectResult
		want string
	}{
		{
			name: "plain",
			in:   CopyObjectResult{ETag: `"c1"`, LastModified: testTime},
			want: wantDecl + `<CopyObjectResult ` + wantNS + `>` +
				`<ETag>&#34;c1&#34;</ETag><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`</CopyObjectResult>`,
		},
		{
			name: "checksums and checksum type",
			in: CopyObjectResult{
				ETag:         `"c1"`,
				LastModified: testTime,
				ChecksumType: "FULL_OBJECT",
				Checksums:    Checksums{SHA1: "c2hhMQ==", CRC64NVME: "AQ=="},
			},
			want: wantDecl + `<CopyObjectResult ` + wantNS + `>` +
				`<ETag>&#34;c1&#34;</ETag><LastModified>2026-01-02T03:04:05.600Z</LastModified>` +
				`<ChecksumCRC64NVME>AQ==</ChecksumCRC64NVME><ChecksumSHA1>c2hhMQ==</ChecksumSHA1>` +
				`<ChecksumType>FULL_OBJECT</ChecksumType>` +
				`</CopyObjectResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendCopyObject(dst, &tc.in) })
		})
	}
}

func TestAppendDeleteResult(t *testing.T) {
	tests := []struct {
		name string
		in   DeleteResult
		want string
	}{
		{
			name: "deleted objects, delete marker and errors",
			in: DeleteResult{
				Deleted: []DeletedObject{
					{Key: "a&b", VersionID: "v1"},
					{Key: "marker/", DeleteMarker: true, DeleteMarkerVersionID: "dm1"},
				},
				Errors: []DeleteError{
					{Key: "locked<k>", VersionID: "v2", Code: "AccessDenied", Message: "Access Denied"},
					{Key: "x", Code: "InternalError", Message: "boom"},
				},
			},
			want: wantDecl + `<DeleteResult ` + wantNS + `>` +
				`<Deleted><Key>a&amp;b</Key><VersionId>v1</VersionId></Deleted>` +
				`<Deleted><Key>marker/</Key><DeleteMarker>true</DeleteMarker><DeleteMarkerVersionId>dm1</DeleteMarkerVersionId></Deleted>` +
				`<Error><Key>locked&lt;k&gt;</Key><VersionId>v2</VersionId><Code>AccessDenied</Code><Message>Access Denied</Message></Error>` +
				`<Error><Key>x</Key><Code>InternalError</Code><Message>boom</Message></Error>` +
				`</DeleteResult>`,
		},
		{
			name: "quiet mode has no deleted entries",
			in:   DeleteResult{},
			want: wantDecl + `<DeleteResult ` + wantNS + `></DeleteResult>`,
		},
		{
			name: "delete marker version id is dropped unless the object is a marker",
			in:   DeleteResult{Deleted: []DeletedObject{{Key: "k", DeleteMarkerVersionID: "dm"}}},
			want: wantDecl + `<DeleteResult ` + wantNS + `><Deleted><Key>k</Key></Deleted></DeleteResult>`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, tc.want, func(dst []byte) []byte { return AppendDeleteResult(dst, &tc.in) })
		})
	}
}

func TestNilArgumentEncodesZeroValue(t *testing.T) {
	tests := []struct {
		name    string
		zero    func(dst []byte) []byte
		withNil func(dst []byte) []byte
	}{
		{
			name:    "ListObjectsV2",
			zero:    func(dst []byte) []byte { return AppendListObjectsV2(dst, &ListObjectsV2Result{}) },
			withNil: func(dst []byte) []byte { return AppendListObjectsV2(dst, nil) },
		},
		{
			name:    "ListObjectsV1",
			zero:    func(dst []byte) []byte { return AppendListObjectsV1(dst, &ListObjectsV1Result{}) },
			withNil: func(dst []byte) []byte { return AppendListObjectsV1(dst, nil) },
		},
		{
			name:    "ListBuckets",
			zero:    func(dst []byte) []byte { return AppendListBuckets(dst, &ListBucketsResult{}) },
			withNil: func(dst []byte) []byte { return AppendListBuckets(dst, nil) },
		},
		{
			name:    "Error",
			zero:    func(dst []byte) []byte { return AppendError(dst, &ErrorResponse{}) },
			withNil: func(dst []byte) []byte { return AppendError(dst, nil) },
		},
		{
			name:    "InitiateMultipartUpload",
			zero:    func(dst []byte) []byte { return AppendInitiateMultipartUpload(dst, &InitiateMultipartUploadResult{}) },
			withNil: func(dst []byte) []byte { return AppendInitiateMultipartUpload(dst, nil) },
		},
		{
			name:    "CompleteMultipartUpload",
			zero:    func(dst []byte) []byte { return AppendCompleteMultipartUpload(dst, &CompleteMultipartUploadResult{}) },
			withNil: func(dst []byte) []byte { return AppendCompleteMultipartUpload(dst, nil) },
		},
		{
			name:    "CopyObject",
			zero:    func(dst []byte) []byte { return AppendCopyObject(dst, &CopyObjectResult{}) },
			withNil: func(dst []byte) []byte { return AppendCopyObject(dst, nil) },
		},
		{
			name:    "DeleteResult",
			zero:    func(dst []byte) []byte { return AppendDeleteResult(dst, &DeleteResult{}) },
			withNil: func(dst []byte) []byte { return AppendDeleteResult(dst, nil) },
		},
		{
			name:    "ListParts",
			zero:    func(dst []byte) []byte { return AppendListParts(dst, &ListPartsResult{}) },
			withNil: func(dst []byte) []byte { return AppendListParts(dst, nil) },
		},
		{
			name:    "ListMultipartUploads",
			zero:    func(dst []byte) []byte { return AppendListMultipartUploads(dst, &ListMultipartUploadsResult{}) },
			withNil: func(dst []byte) []byte { return AppendListMultipartUploads(dst, nil) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkAppend(t, string(tc.zero(nil)), tc.withNil)
		})
	}
}
