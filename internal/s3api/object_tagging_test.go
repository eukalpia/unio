package s3api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/internal/fakes"
)

// s3ErrorCode returns the S3 error code of an SDK error, or "".
func s3ErrorCode(err error) string {
	var apiErr interface{ ErrorCode() string }
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode()
	}
	return ""
}

// s3ErrorStatus returns the HTTP status of an SDK error, or 0.
func s3ErrorStatus(err error) int {
	var respErr *awshttp.ResponseError
	if errors.As(err, &respErr) {
		return respErr.HTTPStatusCode()
	}
	return 0
}

// tagsAsMap returns the tags of a tag set keyed by key.
func tagsAsMap(tags []types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

// setupTaggedObject creates bucket/key with the body data and returns a client.
func setupTaggedObject(t *testing.T, authz auth.Authorizer, bucket, key, data string) *s3.Client {
	t.Helper()
	ctx := context.Background()
	c := newClient(t, authz)
	_, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	must(t, err)
	_, err = c.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), Body: strings.NewReader(data)})
	must(t, err)
	return c
}

// getTags returns the tags of bucket/key as a map.
func getTags(t *testing.T, c *s3.Client, bucket, key string) map[string]string {
	t.Helper()
	out, err := c.GetObjectTagging(context.Background(), &s3.GetObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	must(t, err)
	return tagsAsMap(out.TagSet)
}

// putTags replaces the tags of bucket/key.
func putTags(c *s3.Client, bucket, key string, tags map[string]string) error {
	set := make([]types.Tag, 0, len(tags))
	for k, v := range tags {
		set = append(set, types.Tag{Key: aws.String(k), Value: aws.String(v)})
	}
	_, err := c.PutObjectTagging(context.Background(), &s3.PutObjectTaggingInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		Tagging: &types.Tagging{TagSet: set},
	})
	return err
}

func TestObjectTaggingRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := setupTaggedObject(t, &fakes.Authorizer{}, "tags", "k", "payload")
	before, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("tags"), Key: aws.String("k")})
	must(t, err)

	must(t, putTags(c, "tags", "k", map[string]string{"env": "it", "team": "storage"}))
	if got := getTags(t, c, "tags", "k"); len(got) != 2 || got["env"] != "it" || got["team"] != "storage" {
		t.Fatalf("tags after put = %v", got)
	}

	must(t, putTags(c, "tags", "k", map[string]string{"env": "replaced"}))
	if got := getTags(t, c, "tags", "k"); len(got) != 1 || got["env"] != "replaced" {
		t.Fatalf("tags after replace = %v", got)
	}

	after, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String("tags"), Key: aws.String("k")})
	must(t, err)
	if aws.ToString(after.ETag) != aws.ToString(before.ETag) || !after.LastModified.Equal(*before.LastModified) || aws.ToInt64(after.ContentLength) != aws.ToInt64(before.ContentLength) {
		t.Fatalf("tagging changed the object: before %v %v, after %v %v", before.ETag, before.LastModified, after.ETag, after.LastModified)
	}
	body, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String("tags"), Key: aws.String("k")})
	must(t, err)
	if data := readAll(t, body.Body); string(data) != "payload" {
		t.Fatalf("tagging changed the data: %q", data)
	}

	_, err = c.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("k")})
	must(t, err)
	if got := getTags(t, c, "tags", "k"); len(got) != 0 {
		t.Fatalf("tags after delete = %v, want none", got)
	}
}

func TestObjectTaggingInvalidTags(t *testing.T) {
	ctx := context.Background()
	c := setupTaggedObject(t, &fakes.Authorizer{}, "tags", "k", "x")
	must(t, putTags(c, "tags", "k", map[string]string{"keep": "me"}))

	long := strings.Repeat("k", object.MaxTagKeyLength+1)
	cases := []struct {
		name string
		set  []types.Tag
	}{
		{"reserved prefix", []types.Tag{{Key: aws.String("aws:created-by"), Value: aws.String("x")}}},
		{"duplicate key", []types.Tag{{Key: aws.String("a"), Value: aws.String("1")}, {Key: aws.String("a"), Value: aws.String("2")}}},
		{"empty key", []types.Tag{{Key: aws.String(""), Value: aws.String("x")}}},
		{"key too long", []types.Tag{{Key: aws.String(long), Value: aws.String("x")}}},
		{"value too long", []types.Tag{{Key: aws.String("k"), Value: aws.String(strings.Repeat("v", object.MaxTagValueLength+1))}}},
		{"too many tags", manyTags(object.MaxTags + 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
				Bucket:  aws.String("tags"),
				Key:     aws.String("k"),
				Tagging: &types.Tagging{TagSet: tc.set},
			})
			if code := s3ErrorCode(err); code != "InvalidTag" {
				t.Fatalf("error = %v (code %q), want InvalidTag", err, code)
			}
			if s3ErrorStatus(err) != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", s3ErrorStatus(err))
			}
		})
	}
	if got := getTags(t, c, "tags", "k"); got["keep"] != "me" || len(got) != 1 {
		t.Fatalf("refused tag sets changed the tags: %v", got)
	}
}

func TestObjectTaggingMissingKeyAndVersion(t *testing.T) {
	ctx := context.Background()
	c := setupTaggedObject(t, &fakes.Authorizer{}, "tags", "k", "x")
	set := &types.Tagging{TagSet: []types.Tag{{Key: aws.String("a"), Value: aws.String("b")}}}

	_, err := c.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("absent"), Tagging: set})
	requireSDKError(t, err, http.StatusNotFound, "NoSuchKey")
	_, err = c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("absent")})
	requireSDKError(t, err, http.StatusNotFound, "NoSuchKey")
	_, err = c.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("absent")})
	requireSDKError(t, err, http.StatusNotFound, "NoSuchKey")

	_, err = c.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("k"), VersionId: aws.String("no-such"), Tagging: set})
	requireSDKError(t, err, http.StatusNotFound, "NoSuchVersion")
	_, err = c.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("k"), VersionId: aws.String("no-such")})
	requireSDKError(t, err, http.StatusNotFound, "NoSuchVersion")
	_, err = c.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("k"), VersionId: aws.String("no-such")})
	requireSDKError(t, err, http.StatusNotFound, "NoSuchVersion")
}

func TestObjectTaggingUnauthorized(t *testing.T) {
	ctx := context.Background()
	var denying atomic.Bool
	authz := &fakes.Authorizer{Deny: func(_ auth.Identity, action auth.Action, _, _ string) bool {
		return denying.Load() && (action == auth.ActionPutObjectTagging || action == auth.ActionDeleteObjectTagging)
	}}
	c := setupTaggedObject(t, authz, "tags", "k", "x")
	must(t, putTags(c, "tags", "k", map[string]string{"keep": "me"}))

	denying.Store(true)
	requireSDKError(t, putTags(c, "tags", "k", map[string]string{"other": "1"}), http.StatusForbidden, "AccessDenied")
	_, err := c.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String("tags"), Key: aws.String("k")})
	requireSDKError(t, err, http.StatusForbidden, "AccessDenied")
	denying.Store(false)

	if got := getTags(t, c, "tags", "k"); got["keep"] != "me" || len(got) != 1 {
		t.Fatalf("denied requests changed the tags: %v", got)
	}
	var sawPut, sawDelete bool
	for _, call := range authz.Calls() {
		sawPut = sawPut || call.Action == auth.ActionPutObjectTagging
		sawDelete = sawDelete || call.Action == auth.ActionDeleteObjectTagging
	}
	if !sawPut || !sawDelete {
		t.Fatal("the authorizer was not consulted for the tagging actions")
	}
}

// requireSDKError fails the test unless err is an S3 error with the given
// status and code.
func requireSDKError(t *testing.T, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got no error, want HTTP %d %s", status, code)
	}
	if got := s3ErrorStatus(err); got != status {
		t.Fatalf("HTTP status = %d, want %d (error: %v)", got, status, err)
	}
	if got := s3ErrorCode(err); got != code {
		t.Fatalf("error code = %q, want %q (error: %v)", got, code, err)
	}
}

// manyTags returns n tags with distinct keys.
func manyTags(n int) []types.Tag {
	tags := make([]types.Tag, n)
	for i := range tags {
		tags[i] = types.Tag{Key: aws.String("k" + string(rune('a'+i))), Value: aws.String("v")}
	}
	return tags
}
