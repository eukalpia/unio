package integration

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"
)

// seedCopySource stores a source object with a content type and metadata and
// returns its ETag.
func seedCopySource(ctx context.Context, t *testing.T, cli *s3.Client, bucket, key string, body []byte) string {
	t.Helper()
	out, err := cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/x-unio-test"),
		Metadata:    map[string]string{"origin": "source"},
	})
	if err != nil {
		t.Fatalf("seed source %s/%s: %v", bucket, key, err)
	}
	return aws.ToString(out.ETag)
}

func TestCopyObjectSameBucket(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const src = "src dir/a+b c.txt"
	const dst = "copy/dst.bin"
	body := randomBytes(t, 4096)
	seedCopySource(ctx, t, cli, bucket, src, body)

	_, err := cli.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(bucket),
		Key:        aws.String(dst),
		CopySource: aws.String(copySourceValue(bucket, src)),
	})
	if err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if got := getObjectBytes(ctx, t, cli, bucket, dst); sha256Hex(got) != sha256Hex(body) {
		t.Fatalf("copied body SHA-256 mismatch")
	}
	// The default directive copies content type and user metadata.
	head := headObject(ctx, t, cli, bucket, dst)
	if got := aws.ToString(head.ContentType); got != "application/x-unio-test" {
		t.Errorf("copied Content-Type = %q, want the source value", got)
	}
	if diff := cmp.Diff(map[string]string{"origin": "source"}, lowerKeys(head.Metadata)); diff != "" {
		t.Errorf("copied metadata mismatch (-want +got):\n%s", diff)
	}
}

func TestCopyObjectCrossBucket(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	srcBucket := newBucket(ctx, t, cli)
	dstBucket := newBucket(ctx, t, cli)
	const key = "shared/object.bin"
	body := randomBytes(t, 64<<10)
	seedCopySource(ctx, t, cli, srcBucket, key, body)

	_, err := cli.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:     aws.String(dstBucket),
		Key:        aws.String(key),
		CopySource: aws.String(copySourceValue(srcBucket, key)),
	})
	if err != nil {
		t.Fatalf("CopyObject across buckets: %v", err)
	}
	if got := getObjectBytes(ctx, t, cli, dstBucket, key); sha256Hex(got) != sha256Hex(body) {
		t.Fatalf("copied body SHA-256 mismatch")
	}
	// The source is untouched by a copy.
	if got := getObjectBytes(ctx, t, cli, srcBucket, key); sha256Hex(got) != sha256Hex(body) {
		t.Fatalf("source body changed after copy")
	}
}

func TestCopyObjectReplaceMetadata(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const src = "source"
	const dst = "replaced"
	body := []byte("replace metadata")
	seedCopySource(ctx, t, cli, bucket, src, body)

	_, err := cli.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket:            aws.String(bucket),
		Key:               aws.String(dst),
		CopySource:        aws.String(copySourceValue(bucket, src)),
		MetadataDirective: types.MetadataDirectiveReplace,
		ContentType:       aws.String("text/markdown"),
		Metadata:          map[string]string{"replaced": "yes"},
	})
	if err != nil {
		t.Fatalf("CopyObject REPLACE: %v", err)
	}
	head := headObject(ctx, t, cli, bucket, dst)
	if got := aws.ToString(head.ContentType); got != "text/markdown" {
		t.Errorf("Content-Type = %q, want text/markdown", got)
	}
	if diff := cmp.Diff(map[string]string{"replaced": "yes"}, lowerKeys(head.Metadata)); diff != "" {
		t.Errorf("metadata after REPLACE mismatch (-want +got):\n%s", diff)
	}
	if got := getObjectBytes(ctx, t, cli, bucket, dst); !bytes.Equal(got, body) {
		t.Errorf("body after REPLACE = %q, want %q", got, body)
	}
}

func TestCopyObjectConditionalSource(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const src = "conditional-source"
	etag := seedCopySource(ctx, t, cli, bucket, src, []byte("conditional copy"))

	tests := []struct {
		name       string
		configure  func(*s3.CopyObjectInput)
		wantStatus int
		wantCode   string
	}{
		{
			name:      "copy-source-if-match matches",
			configure: func(in *s3.CopyObjectInput) { in.CopySourceIfMatch = aws.String(etag) },
		},
		{
			name:       "copy-source-if-match differs",
			configure:  func(in *s3.CopyObjectInput) { in.CopySourceIfMatch = aws.String(absentETag) },
			wantStatus: http.StatusPreconditionFailed,
			wantCode:   "PreconditionFailed",
		},
		{
			name:       "copy-source-if-none-match matches",
			configure:  func(in *s3.CopyObjectInput) { in.CopySourceIfNoneMatch = aws.String(etag) },
			wantStatus: http.StatusPreconditionFailed,
			wantCode:   "PreconditionFailed",
		},
		{
			name:      "copy-source-if-none-match differs",
			configure: func(in *s3.CopyObjectInput) { in.CopySourceIfNoneMatch = aws.String(absentETag) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := &s3.CopyObjectInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String("dst-" + tc.name),
				CopySource: aws.String(copySourceValue(bucket, src)),
			}
			tc.configure(in)
			_, err := cli.CopyObject(ctx, in)
			if tc.wantStatus == 0 {
				if err != nil {
					t.Fatalf("CopyObject: %v", err)
				}
				return
			}
			requireAPIError(t, err, tc.wantStatus, tc.wantCode)
		})
	}
}

func TestCopyObjectMissingSourceOrBucket(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	missing := randomBucketName(t)
	seedCopySource(ctx, t, cli, bucket, "present", []byte("p"))

	tests := []struct {
		name       string
		dstBucket  string
		source     string
		wantStatus int
		wantCode   string
	}{
		{name: "missing source key", dstBucket: bucket, source: "absent", wantStatus: http.StatusNotFound, wantCode: "NoSuchKey"},
		{name: "missing destination bucket", dstBucket: missing, source: "present", wantStatus: http.StatusNotFound, wantCode: "NoSuchBucket"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cli.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket:     aws.String(tc.dstBucket),
				Key:        aws.String("dst"),
				CopySource: aws.String(copySourceValue(bucket, tc.source)),
			})
			requireAPIError(t, err, tc.wantStatus, tc.wantCode)
		})
	}
}
