package integration

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/go-cmp/cmp"
)

func TestObjectPutGetHead(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "greeting.txt"
	body := []byte("hello, unio")

	etag := putObject(ctx, t, cli, bucket, key, body)
	if want := md5ETag(body); etag != want {
		t.Errorf("PutObject ETag = %s, want %s", etag, want)
	}

	if diff := cmp.Diff(body, getObjectBytes(ctx, t, cli, bucket, key)); diff != "" {
		t.Errorf("GetObject body mismatch (-want +got):\n%s", diff)
	}

	head := headObject(ctx, t, cli, bucket, key)
	if got := aws.ToInt64(head.ContentLength); got != int64(len(body)) {
		t.Errorf("HeadObject ContentLength = %d, want %d", got, len(body))
	}
	if got := aws.ToString(head.ETag); got != etag {
		t.Errorf("HeadObject ETag = %s, want %s", got, etag)
	}
	if head.LastModified == nil || head.LastModified.IsZero() {
		t.Errorf("HeadObject LastModified is missing")
	}
}

func TestObjectEmpty(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "empty"

	etag := putObject(ctx, t, cli, bucket, key, nil)
	if want := md5ETag(nil); etag != want {
		t.Errorf("PutObject ETag = %s, want %s", etag, want)
	}
	if got := getObjectBytes(ctx, t, cli, bucket, key); len(got) != 0 {
		t.Errorf("GetObject returned %d bytes for an empty object", len(got))
	}
	if got := aws.ToInt64(headObject(ctx, t, cli, bucket, key).ContentLength); got != 0 {
		t.Errorf("HeadObject ContentLength = %d, want 0", got)
	}
}

func TestObjectBinaryRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "random/1mib.bin"
	body := randomBytes(t, 1<<20)

	putObject(ctx, t, cli, bucket, key, body)
	got := getObjectBytes(ctx, t, cli, bucket, key)
	if len(got) != len(body) {
		t.Fatalf("GetObject returned %d bytes, want %d", len(got), len(body))
	}
	if gotSum, wantSum := sha256Hex(got), sha256Hex(body); gotSum != wantSum {
		t.Fatalf("GetObject SHA-256 = %s, want %s", gotSum, wantSum)
	}
	if got := aws.ToInt64(headObject(ctx, t, cli, bucket, key).ContentLength); got != int64(len(body)) {
		t.Errorf("HeadObject ContentLength = %d, want %d", got, len(body))
	}
}

func TestObjectKeyCharacters(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "dir with space/ünïcode+plus&equals=1.txt"
	body := []byte("unusual key")

	putObject(ctx, t, cli, bucket, key, body)
	if diff := cmp.Diff(body, getObjectBytes(ctx, t, cli, bucket, key)); diff != "" {
		t.Errorf("GetObject body mismatch (-want +got):\n%s", diff)
	}
}

func TestObjectKeyLength(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)

	tests := []struct {
		name     string
		key      string
		wantCode string
	}{
		{name: "1024 ASCII bytes", key: strings.Repeat("a", 1024)},
		{name: "1024 bytes of two-byte UTF-8", key: strings.Repeat("é", 512)},
		{name: "1025 ASCII bytes", key: strings.Repeat("a", 1025), wantCode: "KeyTooLongError"},
		{name: "1025 bytes of two-byte UTF-8", key: strings.Repeat("é", 512) + "a", wantCode: "KeyTooLongError"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cli.PutObject(ctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(tc.key),
				Body:   bytes.NewReader([]byte("x")),
			})
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("PutObject: %v", err)
				}
				return
			}
			requireAPIError(t, err, http.StatusBadRequest, tc.wantCode)
		})
	}
}

func TestObjectGetMissingKey(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)

	_, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String("absent")})
	requireAPIError(t, err, http.StatusNotFound, "NoSuchKey")
}

func TestObjectHeadMissingKey(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)

	_, err := cli.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("absent")})
	requireAPIError(t, err, http.StatusNotFound, "NotFound")
}

func TestObjectDelete(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "to-delete"
	putObject(ctx, t, cli, bucket, key, []byte("bye"))

	del := &s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if _, err := cli.DeleteObject(ctx, del); err != nil {
		t.Fatalf("DeleteObject existing key: %v", err)
	}
	_, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	requireAPIError(t, err, http.StatusNotFound, "NoSuchKey")

	// S3 reports success when the key is already absent.
	if _, err := cli.DeleteObject(ctx, del); err != nil {
		t.Errorf("DeleteObject missing key: %v", err)
	}
}

func TestObjectRequestsOnMissingBucket(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	missing := randomBucketName(t)

	tests := []struct {
		name   string
		call   func() error
		status int
		code   string
	}{
		{
			name: "put",
			call: func() error {
				_, err := cli.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(missing), Key: aws.String("k"), Body: strings.NewReader("x")})
				return err
			},
			status: http.StatusNotFound,
			code:   "NoSuchBucket",
		},
		{
			name: "get",
			call: func() error {
				_, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(missing), Key: aws.String("k")})
				return err
			},
			status: http.StatusNotFound,
			code:   "NoSuchBucket",
		},
		{
			name: "head",
			call: func() error {
				_, err := cli.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(missing), Key: aws.String("k")})
				return err
			},
			status: http.StatusNotFound,
			code:   "NotFound",
		},
		{
			name: "delete",
			call: func() error {
				_, err := cli.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(missing), Key: aws.String("k")})
				return err
			},
			status: http.StatusNotFound,
			code:   "NoSuchBucket",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireAPIError(t, tc.call(), tc.status, tc.code)
		})
	}
}

// bodyOf reads the whole body of a GetObject response. It exists so tests that
// need the bytes do not repeat the close and read sequence.
func bodyOf(t *testing.T, out *s3.GetObjectOutput) []byte {
	t.Helper()
	defer out.Body.Close()
	b, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return b
}
