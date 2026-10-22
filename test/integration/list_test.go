package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// listKeys is the object set that every list test runs against. It is kept
// sorted, which is the order S3 returns keys in.
var listKeys = []string{"a/1", "a/2", "b/1", "c", "d/e/f"}

// seedListBucket creates a bucket holding listKeys.
func seedListBucket(ctx context.Context, t *testing.T, cli *s3.Client) string {
	t.Helper()
	bucket := newBucket(ctx, t, cli)
	for _, k := range listKeys {
		putObject(ctx, t, cli, bucket, k, []byte("x"))
	}
	return bucket
}

// objectKeys returns the keys of a listing in order.
func objectKeys(objects []types.Object) []string {
	keys := make([]string, 0, len(objects))
	for _, o := range objects {
		keys = append(keys, aws.ToString(o.Key))
	}
	return keys
}

// prefixNames returns the common prefixes of a listing in order.
func prefixNames(prefixes []types.CommonPrefix) []string {
	names := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		names = append(names, aws.ToString(p.Prefix))
	}
	return names
}

func TestListObjectsV2(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := seedListBucket(ctx, t, cli)

	tests := []struct {
		name         string
		in           s3.ListObjectsV2Input
		wantKeys     []string
		wantPrefixes []string
	}{
		{name: "no filter", wantKeys: listKeys},
		{name: "prefix", in: s3.ListObjectsV2Input{Prefix: aws.String("a/")}, wantKeys: []string{"a/1", "a/2"}},
		{
			name:         "delimiter groups top level",
			in:           s3.ListObjectsV2Input{Delimiter: aws.String("/")},
			wantKeys:     []string{"c"},
			wantPrefixes: []string{"a/", "b/", "d/"},
		},
		{
			name:         "nested prefix with delimiter",
			in:           s3.ListObjectsV2Input{Prefix: aws.String("d/"), Delimiter: aws.String("/")},
			wantPrefixes: []string{"d/e/"},
		},
		{name: "start after", in: s3.ListObjectsV2Input{StartAfter: aws.String("b/1")}, wantKeys: []string{"c", "d/e/f"}},
		{name: "empty result", in: s3.ListObjectsV2Input{Prefix: aws.String("missing/")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := tc.in
			in.Bucket = aws.String(bucket)
			out, err := cli.ListObjectsV2(ctx, &in)
			if err != nil {
				t.Fatalf("ListObjectsV2: %v", err)
			}
			if diff := cmp.Diff(tc.wantKeys, objectKeys(out.Contents), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("keys mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantPrefixes, prefixNames(out.CommonPrefixes), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("common prefixes mismatch (-want +got):\n%s", diff)
			}
			if got, want := aws.ToInt32(out.KeyCount), int32(len(out.Contents)+len(out.CommonPrefixes)); got != want {
				t.Errorf("KeyCount = %d, want %d", got, want)
			}
			if aws.ToBool(out.IsTruncated) {
				t.Errorf("IsTruncated = true for a listing that fits in one page")
			}
		})
	}
}

func TestListObjectsV2Pagination(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := seedListBucket(ctx, t, cli)

	var pages [][]string
	var token *string
	for range 10 {
		out, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			MaxKeys:           aws.Int32(2),
			ContinuationToken: token,
		})
		if err != nil {
			t.Fatalf("ListObjectsV2 page: %v", err)
		}
		pages = append(pages, objectKeys(out.Contents))
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		if out.NextContinuationToken == nil {
			t.Fatal("truncated page has no NextContinuationToken")
		}
		token = out.NextContinuationToken
	}
	want := [][]string{{"a/1", "a/2"}, {"b/1", "c"}, {"d/e/f"}}
	if diff := cmp.Diff(want, pages); diff != "" {
		t.Errorf("pages mismatch (-want +got):\n%s", diff)
	}
}

func TestListObjectsV1(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := seedListBucket(ctx, t, cli)

	tests := []struct {
		name         string
		in           s3.ListObjectsInput
		wantKeys     []string
		wantPrefixes []string
	}{
		{name: "no filter", wantKeys: listKeys},
		{name: "prefix", in: s3.ListObjectsInput{Prefix: aws.String("a/")}, wantKeys: []string{"a/1", "a/2"}},
		{
			name:         "delimiter groups top level",
			in:           s3.ListObjectsInput{Delimiter: aws.String("/")},
			wantKeys:     []string{"c"},
			wantPrefixes: []string{"a/", "b/", "d/"},
		},
		{name: "marker", in: s3.ListObjectsInput{Marker: aws.String("b/1")}, wantKeys: []string{"c", "d/e/f"}},
		{name: "empty result", in: s3.ListObjectsInput{Prefix: aws.String("missing/")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := tc.in
			in.Bucket = aws.String(bucket)
			out, err := cli.ListObjects(ctx, &in)
			if err != nil {
				t.Fatalf("ListObjects: %v", err)
			}
			if diff := cmp.Diff(tc.wantKeys, objectKeys(out.Contents), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("keys mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantPrefixes, prefixNames(out.CommonPrefixes), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("common prefixes mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListObjectsV1Pagination(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := seedListBucket(ctx, t, cli)

	var pages [][]string
	var marker *string
	for range 10 {
		out, err := cli.ListObjects(ctx, &s3.ListObjectsInput{
			Bucket:  aws.String(bucket),
			MaxKeys: aws.Int32(2),
			Marker:  marker,
		})
		if err != nil {
			t.Fatalf("ListObjects page: %v", err)
		}
		keys := objectKeys(out.Contents)
		pages = append(pages, keys)
		if !aws.ToBool(out.IsTruncated) {
			break
		}
		// NextMarker is only returned with a delimiter. Without one, the last
		// key returned is the marker for the next page.
		switch {
		case out.NextMarker != nil:
			marker = out.NextMarker
		case len(keys) > 0:
			marker = aws.String(keys[len(keys)-1])
		default:
			t.Fatal("truncated page has no marker to continue from")
		}
	}
	want := [][]string{{"a/1", "a/2"}, {"b/1", "c"}, {"d/e/f"}}
	if diff := cmp.Diff(want, pages); diff != "" {
		t.Errorf("pages mismatch (-want +got):\n%s", diff)
	}
}

func TestListNegative(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := seedListBucket(ctx, t, cli)
	missing := randomBucketName(t)

	tests := []struct {
		name       string
		call       func() error
		wantStatus int
		wantCode   string
	}{
		{
			name: "v2 missing bucket",
			call: func() error {
				_, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(missing)})
				return err
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "NoSuchBucket",
		},
		{
			name: "v1 missing bucket",
			call: func() error {
				_, err := cli.ListObjects(ctx, &s3.ListObjectsInput{Bucket: aws.String(missing)})
				return err
			},
			wantStatus: http.StatusNotFound,
			wantCode:   "NoSuchBucket",
		},
		{
			name: "v2 malformed continuation token",
			call: func() error {
				_, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
					Bucket:            aws.String(bucket),
					ContinuationToken: aws.String("not a valid token!!"),
				})
				return err
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "InvalidArgument",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireAPIError(t, tc.call(), tc.wantStatus, tc.wantCode)
		})
	}
}
