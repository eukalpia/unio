package fakes

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"strconv"
	"testing"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
)

// compositeETag is the S3 multipart ETag of parts, computed independently
// of the layer: MD5 of the concatenated part digests, then "-<count>".
func compositeETag(parts ...[]byte) string {
	var digests []byte
	for _, p := range parts {
		sum := md5.Sum(p)
		digests = append(digests, sum[:]...)
	}
	sum := md5.Sum(digests)
	return hex.EncodeToString(sum[:]) + "-" + strconv.Itoa(len(parts))
}

// compositeCRC32 is the composite CRC32 of parts, computed independently of
// the layer: CRC32 of the concatenated big-endian part checksums.
func compositeCRC32(parts ...[]byte) []byte {
	var sums []byte
	for _, p := range parts {
		sums = binary.BigEndian.AppendUint32(sums, crc32.ChecksumIEEE(p))
	}
	return binary.BigEndian.AppendUint32(nil, crc32.ChecksumIEEE(sums))
}

// minPart returns a payload of exactly object.MinPartSize bytes.
func minPart() []byte {
	return bytes.Repeat([]byte{'a'}, int(object.MinPartSize))
}

// mustUpload starts an upload of key in testBucket.
func mustUpload(t *testing.T, l *Layer, key string, opts object.PutOptions) string {
	t.Helper()
	id, err := l.NewMultipartUpload(context.Background(), testBucket, key, opts)
	if err != nil {
		t.Fatalf("NewMultipartUpload(%q): %v", key, err)
	}
	return id
}

// mustPart uploads data as part number in upload id of key.
func mustPart(t *testing.T, l *Layer, key, id string, number int, data []byte) object.PartInfo {
	t.Helper()
	p, err := l.PutObjectPart(context.Background(), testBucket, key, id, number, newTestBody(data))
	if err != nil {
		t.Fatalf("PutObjectPart(%d): %v", number, err)
	}
	return p
}

func TestMultipartRoundTrip(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	first := minPart()
	second := []byte("tail")

	id := mustUpload(t, l, "big", object.PutOptions{
		ContentType:       "application/octet-stream",
		UserMetadata:      map[string]string{"owner": "me"},
		ChecksumAlgorithm: object.ChecksumCRC32,
	})
	if len(id) != 32 {
		t.Errorf("upload id %q, want 32 hex characters", id)
	}
	p1 := mustPart(t, l, "big", id, 1, first)
	p2 := mustPart(t, l, "big", id, 2, second)
	if p1.ETag != md5Hex(first) || p1.Size != int64(len(first)) || p2.PartNumber != 2 {
		t.Errorf("part infos = %+v, %+v", p1, p2)
	}

	list, err := l.ListParts(ctx, testBucket, "big", id, object.ListPartsOptions{})
	wantCode(t, err, "")
	if len(list.Parts) != 2 || list.IsTruncated || list.ChecksumAlgorithm != object.ChecksumCRC32 {
		t.Errorf("ListParts = %d parts truncated=%v algorithm=%v, want 2 parts, not truncated, CRC32",
			len(list.Parts), list.IsTruncated, list.ChecksumAlgorithm)
	}

	info, err := l.CompleteMultipartUpload(ctx, testBucket, "big", id, []object.CompletePart{
		{PartNumber: 1, ETag: `"` + p1.ETag + `"`},
		{PartNumber: 2, ETag: p2.ETag},
	}, object.PutOptions{})
	wantCode(t, err, "")

	if want := compositeETag(first, second); info.ETag != want {
		t.Errorf("ETag = %q, want %q", info.ETag, want)
	}
	if info.Size != int64(len(first)+len(second)) || info.Parts != 2 {
		t.Errorf("Size = %d Parts = %d, want %d and 2", info.Size, info.Parts, len(first)+len(second))
	}
	if info.ContentType != "application/octet-stream" || info.UserMetadata["owner"] != "me" {
		t.Errorf("attributes = type %q metadata %v, want the ones given to NewMultipartUpload",
			info.ContentType, info.UserMetadata)
	}
	wantSum := compositeCRC32(first, second)
	if info.Checksum.Algorithm != object.ChecksumCRC32 || info.Checksum.Type != object.ChecksumComposite ||
		info.Checksum.Parts != 2 || !bytes.Equal(info.Checksum.Value, wantSum) {
		t.Errorf("Checksum = %+v, want composite CRC32 over 2 parts", info.Checksum)
	}

	want := append(append([]byte{}, first...), second...)
	if got := mustRead(t, l, "big"); !bytes.Equal(got, want) {
		t.Errorf("completed content has %d bytes, want %d", len(got), len(want))
	}
	head, err := l.HeadObject(ctx, testBucket, "big", object.GetOptions{})
	wantCode(t, err, "")
	if diff := cmp.Diff(info, head); diff != "" {
		t.Errorf("HeadObject differs from CompleteMultipartUpload result (-complete +head):\n%s", diff)
	}

	_, err = l.ListParts(ctx, testBucket, "big", id, object.ListPartsOptions{})
	wantCode(t, err, s3err.NoSuchUpload)
}

func TestCompleteMultipartUploadErrors(t *testing.T) {
	tests := []struct {
		name     string
		parts    func(p1, p2 string) []object.CompletePart
		wantCode s3err.Code
	}{
		{
			name:     "no parts",
			parts:    func(p1, p2 string) []object.CompletePart { return nil },
			wantCode: s3err.InvalidRequest,
		},
		{
			name: "too many parts",
			parts: func(p1, p2 string) []object.CompletePart {
				out := make([]object.CompletePart, object.MaxPartNumber+1)
				for i := range out {
					out[i] = object.CompletePart{PartNumber: i + 1}
				}
				return out
			},
			wantCode: s3err.InvalidRequest,
		},
		{
			name: "descending order",
			parts: func(p1, p2 string) []object.CompletePart {
				return []object.CompletePart{{PartNumber: 2, ETag: p2}, {PartNumber: 1, ETag: p1}}
			},
			wantCode: s3err.InvalidPartOrder,
		},
		{
			name: "duplicate part number",
			parts: func(p1, p2 string) []object.CompletePart {
				return []object.CompletePart{{PartNumber: 1, ETag: p1}, {PartNumber: 1, ETag: p1}}
			},
			wantCode: s3err.InvalidPartOrder,
		},
		{
			name: "part never uploaded",
			parts: func(p1, p2 string) []object.CompletePart {
				return []object.CompletePart{{PartNumber: 1, ETag: p1}, {PartNumber: 3, ETag: p2}}
			},
			wantCode: s3err.InvalidPart,
		},
		{
			name: "part number zero",
			parts: func(p1, p2 string) []object.CompletePart {
				return []object.CompletePart{{PartNumber: 0, ETag: p1}}
			},
			wantCode: s3err.InvalidPart,
		},
		{
			name: "ETag does not match",
			parts: func(p1, p2 string) []object.CompletePart {
				return []object.CompletePart{{PartNumber: 1, ETag: "0000"}, {PartNumber: 2, ETag: p2}}
			},
			wantCode: s3err.InvalidPart,
		},
		{
			name: "matching parts",
			parts: func(p1, p2 string) []object.CompletePart {
				return []object.CompletePart{{PartNumber: 1, ETag: p1}, {PartNumber: 2, ETag: p2}}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			id := mustUpload(t, l, "k", object.PutOptions{})
			p1 := mustPart(t, l, "k", id, 1, minPart())
			p2 := mustPart(t, l, "k", id, 2, []byte("tail"))

			_, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, tt.parts(p1.ETag, p2.ETag), object.PutOptions{})
			wantCode(t, err, tt.wantCode)
			if tt.wantCode != "" {
				if _, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{}); err != nil {
					t.Errorf("failed completion removed the upload: %v", err)
				}
			}
		})
	}
}

func TestCompleteMultipartUploadPartSizes(t *testing.T) {
	tests := []struct {
		name     string
		sizes    []int
		wantCode s3err.Code
	}{
		{name: "small first part", sizes: []int{2, 2}, wantCode: s3err.EntityTooSmall},
		{name: "single small part", sizes: []int{2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			id := mustUpload(t, l, "k", object.PutOptions{})
			var parts []object.CompletePart
			for i, n := range tt.sizes {
				p := mustPart(t, l, "k", id, i+1, bytes.Repeat([]byte{'x'}, n))
				parts = append(parts, object.CompletePart{PartNumber: i + 1, ETag: p.ETag})
			}
			_, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, parts, object.PutOptions{})
			wantCode(t, err, tt.wantCode)
		})
	}
}

func TestCompleteMultipartUploadConditions(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	mustPut(t, l, "k", []byte("existing"))
	id := mustUpload(t, l, "k", object.PutOptions{})
	p := mustPart(t, l, "k", id, 1, []byte("new"))
	parts := []object.CompletePart{{PartNumber: 1, ETag: p.ETag}}

	_, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, parts,
		object.PutOptions{Conditions: object.Conditions{IfNoneMatch: "*"}})
	wantCode(t, err, s3err.PreconditionFailed)
	if got := mustRead(t, l, "k"); string(got) != "existing" {
		t.Errorf("content = %q after failed precondition, want existing", got)
	}

	_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, parts,
		object.PutOptions{Conditions: object.Conditions{IfMatch: md5Hex([]byte("existing"))}})
	wantCode(t, err, "")
	if got := mustRead(t, l, "k"); string(got) != "new" {
		t.Errorf("content = %q after completion, want new", got)
	}
}

func TestPutObjectPartErrors(t *testing.T) {
	tests := []struct {
		name     string
		bucket   string
		key      string
		other    bool // upload belongs to another key
		partNo   int
		body     *testBody
		wantCode s3err.Code
	}{
		{name: "part number zero", partNo: 0, wantCode: s3err.InvalidArgument},
		{name: "part number above maximum", partNo: object.MaxPartNumber + 1, wantCode: s3err.InvalidArgument},
		{name: "upload of another key", partNo: 1, other: true, wantCode: s3err.NoSuchUpload},
		{name: "missing bucket", bucket: "nope", partNo: 1, wantCode: s3err.NoSuchBucket},
		{
			name:     "part too large",
			partNo:   1,
			body:     &testBody{reader: bytes.NewReader(nil), size: object.MaxPartSize + 1},
			wantCode: s3err.EntityTooLarge,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			l := newTestLayer(t)
			id := mustUpload(t, l, "k", object.PutOptions{})
			otherID := mustUpload(t, l, "other", object.PutOptions{})

			bucket := tt.bucket
			if bucket == "" {
				bucket = testBucket
			}
			uploadID := id
			if tt.other {
				uploadID = otherID
			}
			body := tt.body
			if body == nil {
				body = newTestBody([]byte("x"))
			}
			_, err := l.PutObjectPart(ctx, bucket, "k", uploadID, tt.partNo, body)
			wantCode(t, err, tt.wantCode)
		})
	}
}

func TestPutObjectPartUnknownUploadAndBody(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	_, err := l.PutObjectPart(ctx, testBucket, "k", "deadbeef", 1, newTestBody([]byte("x")))
	wantCode(t, err, s3err.NoSuchUpload)

	id := mustUpload(t, l, "k", object.PutOptions{})
	boom := errors.New("aws-chunked signature mismatch")
	_, err = l.PutObjectPart(ctx, testBucket, "k", id, 1, newFailingBody([]byte("partial"), boom))
	if !errors.Is(err, boom) {
		t.Fatalf("PutObjectPart error = %v, want the body error unchanged", err)
	}
	list, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{})
	wantCode(t, err, "")
	if len(list.Parts) != 0 {
		t.Errorf("failed part was stored: %+v", list.Parts)
	}
}

func TestPartOverwriteReplacesPart(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	id := mustUpload(t, l, "k", object.PutOptions{})
	mustPart(t, l, "k", id, 1, []byte("first"))
	mustPart(t, l, "k", id, 1, []byte("second"))

	info, err := l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{{PartNumber: 1, ETag: md5Hex([]byte("second"))}}, object.PutOptions{})
	wantCode(t, err, "")
	if info.Size != int64(len("second")) {
		t.Errorf("Size = %d, want the overwriting part", info.Size)
	}
}

func TestCopyObjectPart(t *testing.T) {
	ctx := context.Background()
	const source = "0123456789"
	tests := []struct {
		name     string
		src      object.Ref
		opts     object.CopyOptions
		upload   string // "" means the upload created by the case
		wantData string
		wantCode s3err.Code
	}{
		{name: "range", src: object.Ref{Bucket: testBucket, Key: "src"}, opts: object.CopyOptions{SourceRange: &object.HTTPRange{Start: 2, End: 5}}, wantData: "2345"},
		{name: "whole object", src: object.Ref{Bucket: testBucket, Key: "src"}, wantData: source},
		{name: "invalid range", src: object.Ref{Bucket: testBucket, Key: "src"}, opts: object.CopyOptions{SourceRange: &object.HTTPRange{Start: 20, End: -1}}, wantCode: s3err.InvalidRange},
		{name: "missing source key", src: object.Ref{Bucket: testBucket, Key: "absent"}, wantCode: s3err.NoSuchKey},
		{
			name:     "source precondition",
			src:      object.Ref{Bucket: testBucket, Key: "src"},
			opts:     object.CopyOptions{SourceConditions: object.Conditions{IfNoneMatch: md5Hex([]byte(source))}},
			wantCode: s3err.PreconditionFailed,
		},
		{name: "unknown upload", src: object.Ref{Bucket: testBucket, Key: "src"}, upload: "deadbeef", wantCode: s3err.NoSuchUpload},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTestLayer(t)
			mustPut(t, l, "src", []byte(source))
			id := tt.upload
			if id == "" {
				id = mustUpload(t, l, "dst", object.PutOptions{})
			}
			p, err := l.CopyObjectPart(ctx, tt.src, testBucket, "dst", id, 1, tt.opts)
			wantCode(t, err, tt.wantCode)
			if tt.wantCode != "" {
				return
			}
			if p.Size != int64(len(tt.wantData)) || p.ETag != md5Hex([]byte(tt.wantData)) {
				t.Errorf("part = size %d etag %q, want size %d etag of %q", p.Size, p.ETag, len(tt.wantData), tt.wantData)
			}
		})
	}
}

func TestListPartsPagination(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	id := mustUpload(t, l, "k", object.PutOptions{ChecksumAlgorithm: object.ChecksumSHA1})
	for n := 1; n <= 5; n++ {
		mustPart(t, l, "k", id, n, []byte{'p', byte('0' + n)})
	}

	tests := []struct {
		name          string
		opts          object.ListPartsOptions
		wantNumbers   []int
		wantTruncated bool
		wantNext      int
		wantCode      s3err.Code
	}{
		{name: "first page", opts: object.ListPartsOptions{MaxParts: 2}, wantNumbers: []int{1, 2}, wantTruncated: true, wantNext: 2},
		{name: "middle page", opts: object.ListPartsOptions{PartNumberMarker: 2, MaxParts: 2}, wantNumbers: []int{3, 4}, wantTruncated: true, wantNext: 4},
		{name: "last page", opts: object.ListPartsOptions{PartNumberMarker: 4, MaxParts: 2}, wantNumbers: []int{5}, wantNext: 5},
		{name: "past the end", opts: object.ListPartsOptions{PartNumberMarker: 5}},
		{name: "default page size", opts: object.ListPartsOptions{}, wantNumbers: []int{1, 2, 3, 4, 5}, wantNext: 5},
		{name: "page size above maximum", opts: object.ListPartsOptions{MaxParts: 5000}, wantNumbers: []int{1, 2, 3, 4, 5}, wantNext: 5},
		{name: "negative page size", opts: object.ListPartsOptions{MaxParts: -1}, wantCode: s3err.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := l.ListParts(ctx, testBucket, "k", id, tt.opts)
			wantCode(t, err, tt.wantCode)
			if tt.wantCode != "" {
				return
			}
			var got []int
			for _, p := range res.Parts {
				got = append(got, p.PartNumber)
			}
			if diff := cmp.Diff(tt.wantNumbers, got); diff != "" {
				t.Errorf("part numbers mismatch (-want +got):\n%s", diff)
			}
			if res.IsTruncated != tt.wantTruncated || res.NextPartNumberMarker != tt.wantNext {
				t.Errorf("truncated=%v next=%d, want truncated=%v next=%d",
					res.IsTruncated, res.NextPartNumberMarker, tt.wantTruncated, tt.wantNext)
			}
			if res.ChecksumAlgorithm != object.ChecksumSHA1 {
				t.Errorf("ChecksumAlgorithm = %v, want SHA1", res.ChecksumAlgorithm)
			}
		})
	}
}

func TestAbortMultipartUpload(t *testing.T) {
	ctx := context.Background()
	l := newTestLayer(t)
	id := mustUpload(t, l, "k", object.PutOptions{})
	mustPart(t, l, "k", id, 1, []byte("x"))

	err := l.AbortMultipartUpload(ctx, testBucket, "other", id)
	wantCode(t, err, s3err.NoSuchUpload)
	if _, err := l.ListParts(ctx, testBucket, "k", id, object.ListPartsOptions{}); err != nil {
		t.Errorf("abort with the wrong key removed the upload: %v", err)
	}

	wantCode(t, l.AbortMultipartUpload(ctx, testBucket, "k", id), "")
	wantCode(t, l.AbortMultipartUpload(ctx, testBucket, "k", id), s3err.NoSuchUpload)
	_, err = l.CompleteMultipartUpload(ctx, testBucket, "k", id, []object.CompletePart{{PartNumber: 1}}, object.PutOptions{})
	wantCode(t, err, s3err.NoSuchUpload)
}
