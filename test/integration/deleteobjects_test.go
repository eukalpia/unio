package integration

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/sync/errgroup"
)

// maxDeleteKeys is the most keys one DeleteObjects request may name.
const maxDeleteKeys = 1000

// deleteObjects sends a DeleteObjects request for keys in bucket.
func deleteObjects(ctx context.Context, cli *s3.Client, bucket string, quiet bool, keys ...string) (*s3.DeleteObjectsOutput, error) {
	ids := make([]types.ObjectIdentifier, 0, len(keys))
	for _, k := range keys {
		ids = append(ids, types.ObjectIdentifier{Key: aws.String(k)})
	}
	return cli.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(quiet)},
	})
}

// deletedKeys returns the sorted keys reported as deleted.
func deletedKeys(deleted []types.DeletedObject) []string {
	keys := make([]string, 0, len(deleted))
	for _, d := range deleted {
		keys = append(keys, aws.ToString(d.Key))
	}
	sort.Strings(keys)
	return keys
}

func TestDeleteObjectsVerbose(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	putObject(ctx, t, cli, bucket, "one", []byte("1"))
	putObject(ctx, t, cli, bucket, "two", []byte("2"))

	out, err := deleteObjects(ctx, cli, bucket, false, "one", "two", "never-existed")
	if err != nil {
		t.Fatalf("DeleteObjects: %v", err)
	}
	// S3 reports an absent key as deleted, so all three keys are listed.
	if diff := cmp.Diff([]string{"never-existed", "one", "two"}, deletedKeys(out.Deleted)); diff != "" {
		t.Errorf("Deleted mismatch (-want +got):\n%s", diff)
	}
	if len(out.Errors) != 0 {
		t.Errorf("Errors = %v, want none", out.Errors)
	}
	if left, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	} else if len(left.Contents) != 0 {
		t.Errorf("%d objects left after DeleteObjects, want 0", len(left.Contents))
	}
}

func TestDeleteObjectsQuiet(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	putObject(ctx, t, cli, bucket, "one", []byte("1"))
	putObject(ctx, t, cli, bucket, "two", []byte("2"))

	out, err := deleteObjects(ctx, cli, bucket, true, "one", "two", "never-existed")
	if err != nil {
		t.Fatalf("DeleteObjects quiet: %v", err)
	}
	if len(out.Deleted) != 0 {
		t.Errorf("quiet mode returned %d Deleted entries, want 0", len(out.Deleted))
	}
	if len(out.Errors) != 0 {
		t.Errorf("Errors = %v, want none", out.Errors)
	}
	if _, err := cli.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String("one")}); err == nil {
		t.Errorf("object one still exists after quiet delete")
	}
}

func TestDeleteObjectsThousandKeys(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)

	keys := make([]string, 0, maxDeleteKeys)
	for i := range maxDeleteKeys {
		keys = append(keys, fmt.Sprintf("bulk/%04d", i))
	}
	if err := putKeysConcurrently(ctx, cli, bucket, keys); err != nil {
		t.Fatalf("seed %d objects: %v", len(keys), err)
	}

	out, err := deleteObjects(ctx, cli, bucket, false, keys...)
	if err != nil {
		t.Fatalf("DeleteObjects with %d keys: %v", len(keys), err)
	}
	if len(out.Deleted) != maxDeleteKeys {
		t.Errorf("Deleted has %d entries, want %d", len(out.Deleted), maxDeleteKeys)
	}
	if len(out.Errors) != 0 {
		t.Errorf("Errors has %d entries, want none", len(out.Errors))
	}
	left, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("ListObjectsV2: %v", err)
	}
	if len(left.Contents) != 0 {
		t.Errorf("%d objects left after DeleteObjects, want 0", len(left.Contents))
	}
}

// putKeysConcurrently stores a small object under each key, with at most 32
// requests in flight. It returns the first error. It takes no *testing.T,
// because a test must not be failed from the goroutines it starts.
func putKeysConcurrently(ctx context.Context, cli *s3.Client, bucket string, keys []string) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(32)
	for _, k := range keys {
		g.Go(func() error {
			_, err := cli.PutObject(gctx, &s3.PutObjectInput{
				Bucket: aws.String(bucket),
				Key:    aws.String(k),
				Body:   bytes.NewReader([]byte(k)),
			})
			if err != nil {
				return fmt.Errorf("put %s: %w", k, err)
			}
			return nil
		})
	}
	return g.Wait()
}

func TestDeleteObjectsNegative(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	missing := randomBucketName(t)

	tooMany := make([]string, 0, maxDeleteKeys+1)
	for i := range maxDeleteKeys + 1 {
		tooMany = append(tooMany, fmt.Sprintf("k%04d", i))
	}

	tests := []struct {
		name       string
		call       func() error
		wantStatus int
		wantCodes  []string
	}{
		{
			// The public docs do not name one code for this case. Implementations
			// return MalformedXML or InvalidArgument, and only the 400 status is
			// fixed by the ceph/s3-tests suite.
			name: "more than 1000 keys",
			call: func() error {
				_, err := deleteObjects(ctx, cli, bucket, true, tooMany...)
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCodes:  []string{"MalformedXML", "InvalidArgument"},
		},
		{
			name: "missing bucket",
			call: func() error {
				_, err := deleteObjects(ctx, cli, missing, true, "k")
				return err
			},
			wantStatus: http.StatusNotFound,
			wantCodes:  []string{"NoSuchBucket"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireAPIErrorOneOf(t, tc.call(), tc.wantStatus, tc.wantCodes...)
		})
	}
}
