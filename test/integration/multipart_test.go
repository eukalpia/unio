package integration

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"
)

// minPartSize is the smallest size S3 accepts for every part except the last.
const minPartSize = 5 << 20

// createUpload starts a multipart upload of key and returns its upload ID.
func createUpload(ctx context.Context, t *testing.T, cli *s3.Client, bucket, key string) string {
	t.Helper()
	out, err := cli.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		t.Fatalf("CreateMultipartUpload %s/%s: %v", bucket, key, err)
	}
	return aws.ToString(out.UploadId)
}

// uploadPart uploads one part of an upload and returns the part's ETag.
func uploadPart(ctx context.Context, t *testing.T, cli *s3.Client, bucket, key, uploadID string, number int32, data []byte) string {
	t.Helper()
	out, err := cli.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		UploadId:      aws.String(uploadID),
		PartNumber:    aws.Int32(number),
		Body:          bytes.NewReader(data),
		ContentLength: aws.Int64(int64(len(data))),
	})
	if err != nil {
		t.Fatalf("UploadPart %d: %v", number, err)
	}
	return aws.ToString(out.ETag)
}

// completedPart pairs a part number with the ETag returned for it.
func completedPart(number int32, etag string) types.CompletedPart {
	return types.CompletedPart{PartNumber: aws.Int32(number), ETag: aws.String(etag)}
}

// completeUpload finishes an upload with the given parts. It returns the raw
// error so that tests can check the code of an expected failure.
func completeUpload(ctx context.Context, cli *s3.Client, bucket, key, uploadID string, parts []types.CompletedPart) (*s3.CompleteMultipartUploadOutput, error) {
	return cli.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bucket),
		Key:             aws.String(key),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
}

// multipartETag returns the ETag S3 gives an object assembled from parts: the
// MD5 of the concatenated binary MD5s of the parts, followed by the part count.
func multipartETag(parts ...[]byte) string {
	var sums []byte
	for _, p := range parts {
		s := md5.Sum(p)
		sums = append(sums, s[:]...)
	}
	whole := md5.Sum(sums)
	return fmt.Sprintf(`"%s-%d"`, hex.EncodeToString(whole[:]), len(parts))
}

func TestMultipartRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "multipart/round-trip"
	part1 := randomBytes(t, minPartSize)
	part2 := randomBytes(t, 1024) // the last part may be smaller than 5 MiB

	id := createUpload(ctx, t, cli, bucket, key)
	e1 := uploadPart(ctx, t, cli, bucket, key, id, 1, part1)
	e2 := uploadPart(ctx, t, cli, bucket, key, id, 2, part2)
	out, err := completeUpload(ctx, cli, bucket, key, id, []types.CompletedPart{
		completedPart(1, e1),
		completedPart(2, e2),
	})
	if err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	if got, want := aws.ToString(out.ETag), multipartETag(part1, part2); got != want {
		t.Errorf("completed ETag = %s, want %s", got, want)
	}
	whole := bytes.Join([][]byte{part1, part2}, nil)
	if got := getObjectBytes(ctx, t, cli, bucket, key); sha256Hex(got) != sha256Hex(whole) {
		t.Fatalf("assembled object SHA-256 mismatch")
	}
}

// partInfo is the subset of a ListParts entry that the tests compare.
type partInfo struct {
	Number int32
	ETag   string
	Size   int64
}

func TestMultipartListParts(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "multipart/list-parts"
	data := [][]byte{randomBytes(t, minPartSize), randomBytes(t, minPartSize), randomBytes(t, 1024)}

	id := createUpload(ctx, t, cli, bucket, key)
	var want []partInfo
	for i, d := range data {
		n := int32(i + 1)
		want = append(want, partInfo{Number: n, ETag: uploadPart(ctx, t, cli, bucket, key, id, n, d), Size: int64(len(d))})
	}

	first, err := cli.ListParts(ctx, &s3.ListPartsInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		UploadId: aws.String(id),
		MaxParts: aws.Int32(2),
	})
	if err != nil {
		t.Fatalf("ListParts page 1: %v", err)
	}
	if !aws.ToBool(first.IsTruncated) {
		t.Errorf("ListParts page 1 IsTruncated = false, want true")
	}
	if got := aws.ToString(first.NextPartNumberMarker); got != "2" {
		t.Errorf("NextPartNumberMarker = %q, want \"2\"", got)
	}
	second, err := cli.ListParts(ctx, &s3.ListPartsInput{
		Bucket:           aws.String(bucket),
		Key:              aws.String(key),
		UploadId:         aws.String(id),
		PartNumberMarker: aws.String("2"),
	})
	if err != nil {
		t.Fatalf("ListParts page 2: %v", err)
	}

	var got []partInfo
	for _, p := range append(first.Parts, second.Parts...) {
		got = append(got, partInfo{Number: aws.ToInt32(p.PartNumber), ETag: aws.ToString(p.ETag), Size: aws.ToInt64(p.Size)})
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListParts mismatch (-want +got):\n%s", diff)
	}
}

func TestMultipartListUploads(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	idOne := createUpload(ctx, t, cli, bucket, "uploads/one")
	idTwo := createUpload(ctx, t, cli, bucket, "uploads/two")

	list := func() map[string]string {
		t.Helper()
		out, err := cli.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{
			Bucket: aws.String(bucket),
			Prefix: aws.String("uploads/"),
		})
		if err != nil {
			t.Fatalf("ListMultipartUploads: %v", err)
		}
		got := make(map[string]string, len(out.Uploads))
		for _, u := range out.Uploads {
			got[aws.ToString(u.Key)] = aws.ToString(u.UploadId)
		}
		return got
	}

	want := map[string]string{"uploads/one": idOne, "uploads/two": idTwo}
	if diff := cmp.Diff(want, list()); diff != "" {
		t.Errorf("uploads before abort mismatch (-want +got):\n%s", diff)
	}
	if _, err := cli.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String("uploads/one"),
		UploadId: aws.String(idOne),
	}); err != nil {
		t.Fatalf("AbortMultipartUpload: %v", err)
	}
	if diff := cmp.Diff(map[string]string{"uploads/two": idTwo}, list()); diff != "" {
		t.Errorf("uploads after abort mismatch (-want +got):\n%s", diff)
	}
}

func TestMultipartAbort(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "multipart/aborted"
	id := createUpload(ctx, t, cli, bucket, key)
	uploadPart(ctx, t, cli, bucket, key, id, 1, []byte("partial"))

	abort := &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(id)}
	if _, err := cli.AbortMultipartUpload(ctx, abort); err != nil {
		t.Fatalf("AbortMultipartUpload: %v", err)
	}

	_, err := cli.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String(bucket), Key: aws.String(key), UploadId: aws.String(id)})
	requireAPIError(t, err, http.StatusNotFound, "NoSuchUpload")

	_, err = cli.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(key),
		UploadId:   aws.String(id),
		PartNumber: aws.Int32(2),
		Body:       bytes.NewReader([]byte("late")),
	})
	requireAPIError(t, err, http.StatusNotFound, "NoSuchUpload")

	_, err = cli.AbortMultipartUpload(ctx, abort)
	requireAPIError(t, err, http.StatusNotFound, "NoSuchUpload")

	_, err = cli.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	requireAPIError(t, err, http.StatusNotFound, "NotFound")
}

func TestMultipartCompleteErrors(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "multipart/invalid-completion"
	big := randomBytes(t, minPartSize)
	small := randomBytes(t, 1024)

	tests := []struct {
		name     string
		parts    [][]byte // part N is parts[N-1]; every part is uploaded first
		complete func(etags []string) []types.CompletedPart
		wantCode string
	}{
		{
			name:  "wrong ETag",
			parts: [][]byte{big, small},
			complete: func(e []string) []types.CompletedPart {
				return []types.CompletedPart{completedPart(1, absentETag), completedPart(2, e[1])}
			},
			wantCode: "InvalidPart",
		},
		{
			name:  "part never uploaded",
			parts: [][]byte{big, small},
			complete: func(e []string) []types.CompletedPart {
				return []types.CompletedPart{completedPart(1, e[0]), completedPart(3, absentETag)}
			},
			wantCode: "InvalidPart",
		},
		{
			name:  "parts out of order",
			parts: [][]byte{big, small},
			complete: func(e []string) []types.CompletedPart {
				return []types.CompletedPart{completedPart(2, e[1]), completedPart(1, e[0])}
			},
			wantCode: "InvalidPartOrder",
		},
		{
			name:  "non-final part below the minimum size",
			parts: [][]byte{small, small},
			complete: func(e []string) []types.CompletedPart {
				return []types.CompletedPart{completedPart(1, e[0]), completedPart(2, e[1])}
			},
			wantCode: "EntityTooSmall",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := createUpload(ctx, t, cli, bucket, key)
			etags := make([]string, len(tc.parts))
			for i, data := range tc.parts {
				etags[i] = uploadPart(ctx, t, cli, bucket, key, id, int32(i+1), data)
			}
			_, err := completeUpload(ctx, cli, bucket, key, id, tc.complete(etags))
			requireAPIError(t, err, http.StatusBadRequest, tc.wantCode)
		})
	}
}

func TestMultipartPartNumberRange(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "multipart/part-numbers"

	tests := []struct {
		name       string
		number     int32
		wantStatus int
		wantCode   string
	}{
		{name: "zero", number: 0, wantStatus: http.StatusBadRequest, wantCode: "InvalidArgument"},
		{name: "above 10000", number: 10001, wantStatus: http.StatusBadRequest, wantCode: "InvalidArgument"},
		{name: "10000 is accepted", number: 10000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := createUpload(ctx, t, cli, bucket, key)
			_, err := cli.UploadPart(ctx, &s3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(id),
				PartNumber: aws.Int32(tc.number),
				Body:       bytes.NewReader([]byte("p")),
			})
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("UploadPart %d: %v", tc.number, err)
				}
				return
			}
			requireAPIError(t, err, tc.wantStatus, tc.wantCode)
		})
	}
}

func TestUploadPartCopy(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const src = "source/six-mib"
	const dst = "copy/assembled"
	source := randomBytes(t, 6<<20)
	putObject(ctx, t, cli, bucket, src, source)

	id := createUpload(ctx, t, cli, bucket, dst)
	copyPart := func(number int32, rng string) string {
		t.Helper()
		out, err := cli.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
			Bucket:          aws.String(bucket),
			Key:             aws.String(dst),
			UploadId:        aws.String(id),
			PartNumber:      aws.Int32(number),
			CopySource:      aws.String(copySourceValue(bucket, src)),
			CopySourceRange: aws.String(rng),
		})
		if err != nil {
			t.Fatalf("UploadPartCopy %d (%s): %v", number, rng, err)
		}
		return aws.ToString(out.CopyPartResult.ETag)
	}
	e1 := copyPart(1, "bytes=0-5242879")
	e2 := copyPart(2, "bytes=5242880-6291455")
	if _, err := completeUpload(ctx, cli, bucket, dst, id, []types.CompletedPart{
		completedPart(1, e1),
		completedPart(2, e2),
	}); err != nil {
		t.Fatalf("CompleteMultipartUpload: %v", err)
	}
	if got := getObjectBytes(ctx, t, cli, bucket, dst); sha256Hex(got) != sha256Hex(source) {
		t.Fatalf("assembled object SHA-256 mismatch")
	}
}
