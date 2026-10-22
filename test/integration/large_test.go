package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	// largeObjectSize is 100 MiB.
	largeObjectSize = 100 << 20
	// largePartSize is the part size of the large upload.
	largePartSize = 16 << 20
	// largeTestTimeout bounds the 100 MiB round trip.
	largeTestTimeout = 10 * time.Minute
)

func TestLargeObjectMultipart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the 100 MiB multipart upload in -short mode")
	}
	t.Parallel()
	ctx, cli := setupWithTimeout(t, largeTestTimeout)
	bucket := newBucket(ctx, t, cli)
	const key = "large/100mib.bin"
	data := randomBytes(t, largeObjectSize)

	id := createUpload(ctx, t, cli, bucket, key)
	var parts []types.CompletedPart
	var partBodies [][]byte
	for offset, n := 0, int32(1); offset < len(data); offset, n = offset+largePartSize, n+1 {
		end := min(offset+largePartSize, len(data))
		chunk := data[offset:end]
		partBodies = append(partBodies, chunk)
		etag := uploadPart(ctx, t, cli, bucket, key, id, n, chunk)
		parts = append(parts, completedPart(n, etag))
	}
	out, err := completeUpload(ctx, cli, bucket, key, id, parts)
	if err != nil {
		t.Fatalf("CompleteMultipartUpload with %d parts: %v", len(parts), err)
	}

	wantETag := multipartETag(partBodies...)
	if got := aws.ToString(out.ETag); got != wantETag {
		t.Errorf("completed ETag = %s, want %s", got, wantETag)
	}
	if got := aws.ToInt64(headObject(ctx, t, cli, bucket, key).ContentLength); got != largeObjectSize {
		t.Errorf("HeadObject ContentLength = %d, want %d", got, largeObjectSize)
	}

	// Stream the object and hash it, instead of holding a second 100 MiB copy.
	get, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer get.Body.Close()
	h := sha256.New()
	n, err := io.Copy(h, get.Body)
	if err != nil {
		t.Fatalf("stream object body: %v", err)
	}
	if n != largeObjectSize {
		t.Fatalf("streamed %d bytes, want %d", n, largeObjectSize)
	}
	if got, want := hex.EncodeToString(h.Sum(nil)), sha256Hex(data); got != want {
		t.Fatalf("streamed SHA-256 = %s, want %s", got, want)
	}
}
