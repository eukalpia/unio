package integration

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// cleanupTimeout bounds the work that removes a test's bucket.
const cleanupTimeout = 5 * time.Minute

// bucketPrefix starts every bucket name the suite creates.
const bucketPrefix = "unio-it-"

// randomBucketName returns "unio-it-" followed by 12 random lowercase hex
// characters taken from crypto/rand.
func randomBucketName(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("read random bucket suffix: %v", err)
	}
	return bucketPrefix + hex.EncodeToString(b[:])
}

// createBucket creates bucket in the client's region. Regions other than
// us-east-1 need an explicit location constraint.
func createBucket(ctx context.Context, cli *s3.Client, bucket string) error {
	in := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	if region := cli.Options().Region; region != defaultRegion {
		in.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(region),
		}
	}
	if _, err := cli.CreateBucket(ctx, in); err != nil {
		return fmt.Errorf("create bucket %s: %w", bucket, err)
	}
	return nil
}

// newBucket creates a bucket with a random name and registers its removal with
// t.Cleanup. It returns the bucket name.
func newBucket(ctx context.Context, t *testing.T, cli *s3.Client) string {
	t.Helper()
	name := randomBucketName(t)
	if err := createBucket(ctx, cli, name); err != nil {
		t.Fatalf("%v", err)
	}
	cleanupBucket(t, cli, name)
	return name
}

// cleanupBucket registers t.Cleanup to remove bucket, along with its objects
// and multipart uploads, however the test ends.
func cleanupBucket(t *testing.T, cli *s3.Client, bucket string) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		removeBucket(ctx, t, cli, bucket)
	})
}

// removeBucket deletes the multipart uploads and objects in bucket and then the
// bucket. A bucket the test already deleted is not an error. Other failures are
// reported with t.Errorf so that a leaked bucket fails the run.
func removeBucket(ctx context.Context, t *testing.T, cli *s3.Client, bucket string) {
	t.Helper()
	if err := abortAllUploads(ctx, cli, bucket); err != nil {
		if errorCode(err) == "NoSuchBucket" {
			return
		}
		t.Errorf("cleanup: abort uploads in %s: %v", bucket, err)
		return
	}
	if err := deleteAllObjects(ctx, cli, bucket); err != nil {
		t.Errorf("cleanup: empty %s: %v", bucket, err)
		return
	}
	if _, err := cli.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil && errorCode(err) != "NoSuchBucket" {
		t.Errorf("cleanup: delete bucket %s: %v", bucket, err)
	}
}

// abortAllUploads aborts every in-progress multipart upload in bucket. It lists
// from the start each time, since aborted uploads drop out of the listing.
func abortAllUploads(ctx context.Context, cli *s3.Client, bucket string) error {
	for {
		out, err := cli.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket)})
		if err != nil {
			return fmt.Errorf("list multipart uploads: %w", err)
		}
		if len(out.Uploads) == 0 {
			return nil
		}
		for _, u := range out.Uploads {
			_, err := cli.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      u.Key,
				UploadId: u.UploadId,
			})
			if err != nil {
				return fmt.Errorf("abort upload %s: %w", aws.ToString(u.Key), err)
			}
		}
	}
}

// deleteAllObjects removes every object in bucket in batches of up to 1000.
func deleteAllObjects(ctx context.Context, cli *s3.Client, bucket string) error {
	for {
		out, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err != nil {
			return fmt.Errorf("list objects: %w", err)
		}
		if len(out.Contents) == 0 {
			return nil
		}
		ids := make([]types.ObjectIdentifier, 0, len(out.Contents))
		for _, o := range out.Contents {
			ids = append(ids, types.ObjectIdentifier{Key: o.Key})
		}
		del, err := cli.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
		})
		if err != nil {
			return fmt.Errorf("delete objects: %w", err)
		}
		if len(del.Errors) > 0 {
			return fmt.Errorf("delete objects: %d failed, first code %s", len(del.Errors), aws.ToString(del.Errors[0].Code))
		}
	}
}

// putObject stores body under key with a single PUT and returns the ETag.
func putObject(ctx context.Context, t *testing.T, cli *s3.Client, bucket, key string, body []byte) string {
	t.Helper()
	out, err := cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		t.Fatalf("PutObject %s/%s: %v", bucket, key, err)
	}
	return aws.ToString(out.ETag)
}

// getObjectBytes returns the full body of an object and fails t on error.
func getObjectBytes(ctx context.Context, t *testing.T, cli *s3.Client, bucket, key string) []byte {
	t.Helper()
	out, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject %s/%s: %v", bucket, key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatalf("read body of %s/%s: %v", bucket, key, err)
	}
	return body
}

// headObject returns the metadata of an object and fails t on error.
func headObject(ctx context.Context, t *testing.T, cli *s3.Client, bucket, key string) *s3.HeadObjectOutput {
	t.Helper()
	out, err := cli.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("HeadObject %s/%s: %v", bucket, key, err)
	}
	return out
}

// md5ETag returns the quoted hex MD5 that S3 reports as the ETag of a
// single-part upload.
func md5ETag(data []byte) string {
	sum := md5.Sum(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// randomBytes returns n bytes from crypto/rand.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("read %d random bytes: %v", n, err)
	}
	return b
}

// sha256Hex returns the lowercase hex SHA-256 digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// copySourceValue builds the x-amz-copy-source value for bucket/key. Each path
// segment of the key is percent-encoded, with '+' escaped too, and the slashes
// between segments are kept.
func copySourceValue(bucket, key string) string {
	segments := strings.Split(key, "/")
	for i, s := range segments {
		segments[i] = strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
	}
	return bucket + "/" + strings.Join(segments, "/")
}

// errorCode returns the S3 error code carried by err, or "" when err has none.
func errorCode(err error) string {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// errorStatus returns the HTTP status of the response behind err, or 0.
func errorStatus(err error) int {
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode()
	}
	return 0
}

// requireAPIError fails t unless err is an S3 error with the given HTTP status
// and error code. Pass an empty code to check only the status, which is the
// case for HEAD responses, whose bodies are empty.
func requireAPIError(t *testing.T, err error, wantStatus int, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want HTTP %d %s", wantStatus, wantCode)
	}
	if got := errorStatus(err); got != wantStatus {
		t.Fatalf("HTTP status = %d, want %d (error: %v)", got, wantStatus, err)
	}
	if wantCode != "" {
		if got := errorCode(err); got != wantCode {
			t.Fatalf("error code = %q, want %q (error: %v)", got, wantCode, err)
		}
	}
}

// requireAPIErrorOneOf fails t unless err is an S3 error with the given HTTP
// status and one of the given error codes. It is for cases where the public
// documentation does not pin a single code.
func requireAPIErrorOneOf(t *testing.T, err error, wantStatus int, wantCodes ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want HTTP %d %v", wantStatus, wantCodes)
	}
	if got := errorStatus(err); got != wantStatus {
		t.Fatalf("HTTP status = %d, want %d (error: %v)", got, wantStatus, err)
	}
	got := errorCode(err)
	for _, c := range wantCodes {
		if got == c {
			return
		}
	}
	t.Fatalf("error code = %q, want one of %q (error: %v)", got, wantCodes, err)
}

// waitUntil blocks until deadline has passed. It returns the context's error if
// ctx ends first. Tests use it to let a presigned URL expire without a sleep.
func waitUntil(ctx context.Context, deadline time.Time) error {
	wait, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	<-wait.Done()
	return ctx.Err()
}
