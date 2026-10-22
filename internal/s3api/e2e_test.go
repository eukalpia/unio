package s3api_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3api"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// TestMain verifies that no goroutine outlives the tests. Hertz starts one
// process-wide goroutine that refreshes the Date header and never exits; it is
// ignored by name, because it is a library singleton and not a leak.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreAnyFunction("github.com/cloudwego/hertz/pkg/protocol.UpdateServerDate.func1"))
}

// newClient starts a server on a random localhost port backed by the fakes and
// returns an AWS SDK S3 client pointed at it. The server is stopped and the
// connections are closed when the test ends.
func newClient(t *testing.T, authz auth.Authorizer) *s3.Client {
	t.Helper()
	return newS3(startServer(t, s3api.Config{}, authz), newHTTPClient(t, nil))
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, r io.ReadCloser) []byte {
	t.Helper()
	defer r.Close()
	b, err := io.ReadAll(r)
	must(t, err)
	return b
}

// patterned returns n bytes with a non-repeating pattern, so that offset
// mistakes are visible in comparisons.
func patterned(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*131 + i/251)
	}
	return b
}

func TestBucketAndObjectRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newClient(t, &fakes.Authorizer{})

	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("photos")})
	must(t, err)
	_, err = c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String("photos")})
	must(t, err)
	lb, err := c.ListBuckets(ctx, &s3.ListBucketsInput{})
	must(t, err)
	if len(lb.Buckets) != 1 || aws.ToString(lb.Buckets[0].Name) != "photos" {
		t.Fatalf("buckets = %+v", lb.Buckets)
	}
	loc, err := c.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String("photos")})
	must(t, err)
	if loc.LocationConstraint != "" {
		t.Fatalf("us-east-1 location = %q, want empty", loc.LocationConstraint)
	}

	data := patterned(10000)
	sum := md5.Sum(data)
	_, err = c.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String("photos"),
		Key:         aws.String("a/b.txt"),
		Body:        bytes.NewReader(data),
		ContentType: aws.String("text/plain"),
		Metadata:    map[string]string{"color": "blue"},
	})
	must(t, err)

	got, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("a/b.txt")})
	must(t, err)
	if !bytes.Equal(readAll(t, got.Body), data) {
		t.Fatal("GET returned different bytes")
	}
	if aws.ToString(got.ContentType) != "text/plain" || got.Metadata["color"] != "blue" {
		t.Fatalf("content type %q metadata %v", aws.ToString(got.ContentType), got.Metadata)
	}
	if want := `"` + hex.EncodeToString(sum[:]) + `"`; aws.ToString(got.ETag) != want {
		t.Fatalf("ETag = %s, want %s", aws.ToString(got.ETag), want)
	}

	ranged, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("a/b.txt"), Range: aws.String("bytes=2-5")})
	must(t, err)
	if !bytes.Equal(readAll(t, ranged.Body), data[2:6]) || aws.ToString(ranged.ContentRange) != "bytes 2-5/10000" {
		t.Fatalf("range read: content-range %q", aws.ToString(ranged.ContentRange))
	}

	head, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("photos"), Key: aws.String("a/b.txt")})
	must(t, err)
	if aws.ToInt64(head.ContentLength) != int64(len(data)) {
		t.Fatalf("HEAD length = %d", aws.ToInt64(head.ContentLength))
	}

	_, err = c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("missing")})
	var nsk *types.NoSuchKey
	if !errors.As(err, &nsk) {
		t.Fatalf("missing key: got %v, want NoSuchKey", err)
	}

	_, err = c.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String("photos"),
		Key:        aws.String("copy/of b.txt"),
		CopySource: aws.String("photos/a/b.txt"),
	})
	must(t, err)
	cp, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("photos"), Key: aws.String("copy/of b.txt")})
	must(t, err)
	if !bytes.Equal(readAll(t, cp.Body), data) {
		t.Fatal("copied object differs")
	}

	_, err = c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String("photos")})
	if !strings.Contains(fmt.Sprint(err), "BucketNotEmpty") {
		t.Fatalf("delete of a non-empty bucket: %v", err)
	}
	del, err := c.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String("photos"),
		Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String("a/b.txt")}, {Key: aws.String("copy/of b.txt")}}},
	})
	must(t, err)
	if len(del.Deleted) != 2 || len(del.Errors) != 0 {
		t.Fatalf("DeleteObjects deleted %d, errors %v", len(del.Deleted), del.Errors)
	}
	_, err = c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("photos"), Key: aws.String("never-existed")})
	must(t, err)
	_, err = c.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String("photos")})
	must(t, err)
}

func TestListingPaginates(t *testing.T) {
	ctx := context.Background()
	c := newClient(t, &fakes.Authorizer{})
	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("lists")})
	must(t, err)
	for _, k := range []string{"d/1", "d/2", "e/1", "f", "g/x/1", "g/x/2"} {
		_, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("lists"), Key: aws.String(k), Body: strings.NewReader("v")})
		must(t, err)
	}

	var keys []string
	pager := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{Bucket: aws.String("lists"), MaxKeys: aws.Int32(2)})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		must(t, err)
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	if strings.Join(keys, ",") != "d/1,d/2,e/1,f,g/x/1,g/x/2" {
		t.Fatalf("paged keys = %v", keys)
	}

	dl, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("lists"), Delimiter: aws.String("/"), Prefix: aws.String("")})
	must(t, err)
	var prefixes []string
	for _, p := range dl.CommonPrefixes {
		prefixes = append(prefixes, aws.ToString(p.Prefix))
	}
	if strings.Join(prefixes, ",") != "d/,e/,g/" || len(dl.Contents) != 1 || aws.ToString(dl.Contents[0].Key) != "f" {
		t.Fatalf("delimiter listing: prefixes %v contents %d", prefixes, len(dl.Contents))
	}

	v1, err := c.ListObjects(ctx, &s3.ListObjectsInput{Bucket: aws.String("lists"), MaxKeys: aws.Int32(3)})
	must(t, err)
	if !aws.ToBool(v1.IsTruncated) || len(v1.Contents) != 3 {
		t.Fatalf("V1 page: truncated %v, %d keys", aws.ToBool(v1.IsTruncated), len(v1.Contents))
	}
	v1next, err := c.ListObjects(ctx, &s3.ListObjectsInput{Bucket: aws.String("lists"), Marker: aws.String(aws.ToString(v1.Contents[2].Key))})
	must(t, err)
	if len(v1next.Contents) != 3 || aws.ToString(v1next.Contents[0].Key) != "f" {
		t.Fatalf("V1 continuation starts at %v", v1next.Contents)
	}
}

func TestMultipartUploadWithCopiedPart(t *testing.T) {
	ctx := context.Background()
	c := newClient(t, &fakes.Authorizer{})
	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("multipart")})
	must(t, err)

	first := patterned(5 << 20)
	_, err = c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("multipart"), Key: aws.String("src"), Body: bytes.NewReader(first)})
	must(t, err)

	created, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("multipart"), Key: aws.String("dst")})
	must(t, err)
	uploadID := created.UploadId

	copied, err := c.UploadPartCopy(ctx, &s3.UploadPartCopyInput{
		Bucket:     aws.String("multipart"),
		Key:        aws.String("dst"),
		UploadId:   uploadID,
		PartNumber: aws.Int32(1),
		CopySource: aws.String("multipart/src"),
	})
	must(t, err)
	tail := patterned(1024)
	tailPart, err := c.UploadPart(ctx, &s3.UploadPartInput{
		Bucket:     aws.String("multipart"),
		Key:        aws.String("dst"),
		UploadId:   uploadID,
		PartNumber: aws.Int32(2),
		Body:       bytes.NewReader(tail),
	})
	must(t, err)

	parts, err := c.ListParts(ctx, &s3.ListPartsInput{Bucket: aws.String("multipart"), Key: aws.String("dst"), UploadId: uploadID})
	must(t, err)
	if len(parts.Parts) != 2 {
		t.Fatalf("ListParts returned %d parts", len(parts.Parts))
	}

	uploads, err := c.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String("multipart")})
	must(t, err)
	if len(uploads.Uploads) != 1 {
		t.Fatalf("in-progress uploads = %d", len(uploads.Uploads))
	}

	done, err := c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String("multipart"),
		Key:      aws.String("dst"),
		UploadId: uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{
			{PartNumber: aws.Int32(1), ETag: copied.CopyPartResult.ETag},
			{PartNumber: aws.Int32(2), ETag: tailPart.ETag},
		}},
	})
	must(t, err)
	if !strings.HasSuffix(strings.Trim(aws.ToString(done.ETag), `"`), "-2") {
		t.Fatalf("composite ETag = %s", aws.ToString(done.ETag))
	}

	got, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("multipart"), Key: aws.String("dst")})
	must(t, err)
	if !bytes.Equal(readAll(t, got.Body), append(append([]byte{}, first...), tail...)) {
		t.Fatal("completed object differs from its parts")
	}
}

func TestMultipartAbort(t *testing.T) {
	ctx := context.Background()
	c := newClient(t, &fakes.Authorizer{})
	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("abort")})
	must(t, err)
	created, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String("abort"), Key: aws.String("k")})
	must(t, err)
	_, err = c.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String("abort"), Key: aws.String("k"), UploadId: created.UploadId})
	must(t, err)
	_, err = c.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String("abort"), Key: aws.String("k"), UploadId: created.UploadId})
	if !strings.Contains(fmt.Sprint(err), "NoSuchUpload") {
		t.Fatalf("second abort: %v", err)
	}
}

func TestAuthorizationDenied(t *testing.T) {
	ctx := context.Background()
	deny := &fakes.Authorizer{Deny: func(_ auth.Identity, action auth.Action, _, _ string) bool {
		return action == auth.ActionPutObject
	}}
	c := newClient(t, deny)
	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("locked")})
	must(t, err)
	_, err = c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("locked"), Key: aws.String("k"), Body: strings.NewReader("x")})
	if !strings.Contains(fmt.Sprint(err), "AccessDenied") {
		t.Fatalf("denied PUT: %v", err)
	}
	if len(deny.Calls()) == 0 {
		t.Fatal("authorizer was not consulted")
	}
}

func TestChecksumRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := newClient(t, &fakes.Authorizer{})
	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("sums")})
	must(t, err)
	data := []byte("checksummed payload")
	_, err = c.PutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String("sums"),
		Key:               aws.String("k"),
		Body:              bytes.NewReader(data),
		ChecksumAlgorithm: types.ChecksumAlgorithmCrc32,
	})
	must(t, err)
	got, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("sums"), Key: aws.String("k"), ChecksumMode: types.ChecksumModeEnabled})
	must(t, err)
	readAll(t, got.Body)
	if aws.ToString(got.ChecksumCRC32) == "" {
		t.Fatal("GET with checksum mode returned no CRC32")
	}
}
