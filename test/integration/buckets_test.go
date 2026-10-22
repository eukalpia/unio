package integration

import (
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestBucketCreateHeadDelete(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	name := randomBucketName(t)
	cleanupBucket(t, cli, name)

	if err := createBucket(ctx, cli, name); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatalf("HeadBucket after create: %v", err)
	}
	if _, err := cli.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)}); err != nil {
		t.Fatalf("DeleteBucket: %v", err)
	}
	_, err := cli.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(name)})
	requireAPIError(t, err, http.StatusNotFound, "NotFound")
}

func TestBucketCreateDuplicate(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	name := newBucket(ctx, t, cli)

	err := createBucket(ctx, cli, name)
	if err == nil && cli.Options().Region == defaultRegion {
		// AWS S3 in us-east-1 lets the owner re-create a bucket and returns 200.
		// Every other region returns BucketAlreadyOwnedByYou.
		return
	}
	requireAPIError(t, err, http.StatusConflict, "BucketAlreadyOwnedByYou")
}

func TestBucketCreateInvalidName(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)

	tests := []struct {
		name   string
		bucket string
	}{
		{name: "too short", bucket: "ab"},
		{name: "upper case", bucket: "UnioBucket"},
		{name: "underscore", bucket: "under_score"},
		{name: "leading hyphen", bucket: "-leading"},
		{name: "trailing hyphen", bucket: "trailing-"},
		{name: "IP address form", bucket: "192.168.0.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := createBucket(ctx, cli, tc.bucket)
			if err == nil {
				// The server accepted an invalid name. Remove the bucket so the
				// run does not leak it. The assertion below still fails the test.
				cleanupBucket(t, cli, tc.bucket)
			}
			requireAPIError(t, err, http.StatusBadRequest, "InvalidBucketName")
		})
	}
}

func TestBucketListIncludesCreated(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	first := newBucket(ctx, t, cli)
	second := newBucket(ctx, t, cli)

	out, err := cli.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		t.Fatalf("ListBuckets: %v", err)
	}
	names := make(map[string]bool, len(out.Buckets))
	for _, b := range out.Buckets {
		names[aws.ToString(b.Name)] = true
	}
	for _, want := range []string{first, second} {
		if !names[want] {
			t.Errorf("ListBuckets does not include %s", want)
		}
	}
}

func TestBucketLocation(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	name := newBucket(ctx, t, cli)

	out, err := cli.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(name)})
	if err != nil {
		t.Fatalf("GetBucketLocation: %v", err)
	}
	region := cli.Options().Region
	got := string(out.LocationConstraint)
	if region == defaultRegion {
		// S3 reports us-east-1 as an empty location constraint.
		if got != "" && got != defaultRegion {
			t.Fatalf("GetBucketLocation = %q, want an empty constraint or %q", got, defaultRegion)
		}
		return
	}
	if got != region {
		t.Fatalf("GetBucketLocation = %q, want %q", got, region)
	}
}

func TestBucketDeleteNonEmpty(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	name := newBucket(ctx, t, cli)
	putObject(ctx, t, cli, name, "kept", []byte("data"))

	_, err := cli.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(name)})
	requireAPIError(t, err, http.StatusConflict, "BucketNotEmpty")
}

func TestBucketMissing(t *testing.T) {
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
			name: "head",
			call: func() error {
				_, err := cli.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(missing)})
				return err
			},
			status: http.StatusNotFound,
			code:   "NotFound",
		},
		{
			name: "get location",
			call: func() error {
				_, err := cli.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(missing)})
				return err
			},
			status: http.StatusNotFound,
			code:   "NoSuchBucket",
		},
		{
			name: "list objects",
			call: func() error {
				_, err := cli.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(missing)})
				return err
			},
			status: http.StatusNotFound,
			code:   "NoSuchBucket",
		},
		{
			name: "delete",
			call: func() error {
				_, err := cli.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(missing)})
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
