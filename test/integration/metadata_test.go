package integration

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/go-cmp/cmp"
)

// lowerKeys returns m with every key lower-cased. S3 returns user metadata
// names in lower case, so comparisons normalise the case of the keys.
func lowerKeys(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(k)] = v
	}
	return out
}

func TestObjectUserMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "with-metadata"
	want := map[string]string{"owner": "storage-team", "build-id": "42"}

	_, err := cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:   aws.String(bucket),
		Key:      aws.String(key),
		Body:     bytes.NewReader([]byte("m")),
		Metadata: map[string]string{"Owner": "storage-team", "Build-Id": "42"},
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	get, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	bodyOf(t, get)
	if diff := cmp.Diff(want, lowerKeys(get.Metadata)); diff != "" {
		t.Errorf("GetObject metadata mismatch (-want +got):\n%s", diff)
	}

	head := headObject(ctx, t, cli, bucket, key)
	if diff := cmp.Diff(want, lowerKeys(head.Metadata)); diff != "" {
		t.Errorf("HeadObject metadata mismatch (-want +got):\n%s", diff)
	}
}

// httpHeaders is the set of standard object headers that the suite round-trips.
type httpHeaders struct {
	ContentType        string
	CacheControl       string
	ContentDisposition string
	ContentEncoding    string
	ContentLanguage    string
	Expires            string
}

func headersFromHead(h *s3.HeadObjectOutput) httpHeaders {
	return httpHeaders{
		ContentType:        aws.ToString(h.ContentType),
		CacheControl:       aws.ToString(h.CacheControl),
		ContentDisposition: aws.ToString(h.ContentDisposition),
		ContentEncoding:    aws.ToString(h.ContentEncoding),
		ContentLanguage:    aws.ToString(h.ContentLanguage),
		Expires:            aws.ToString(h.ExpiresString),
	}
}

func headersFromGet(g *s3.GetObjectOutput) httpHeaders {
	return httpHeaders{
		ContentType:        aws.ToString(g.ContentType),
		CacheControl:       aws.ToString(g.CacheControl),
		ContentDisposition: aws.ToString(g.ContentDisposition),
		ContentEncoding:    aws.ToString(g.ContentEncoding),
		ContentLanguage:    aws.ToString(g.ContentLanguage),
		Expires:            aws.ToString(g.ExpiresString),
	}
}

func TestObjectHTTPHeadersRoundTrip(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "with-headers"
	expires := time.Date(2034, time.March, 4, 5, 6, 7, 0, time.UTC)
	// ContentEncoding is "identity" so that Go's transport never tries to
	// decompress the body, which is stored as plain bytes.
	want := httpHeaders{
		ContentType:        "text/plain; charset=utf-8",
		CacheControl:       "max-age=60, public",
		ContentDisposition: `attachment; filename="report.txt"`,
		ContentEncoding:    "identity",
		ContentLanguage:    "en-GB",
		Expires:            expires.Format(http.TimeFormat),
	}

	_, err := cli.PutObject(ctx, &s3.PutObjectInput{
		Bucket:             aws.String(bucket),
		Key:                aws.String(key),
		Body:               bytes.NewReader([]byte("headers")),
		ContentType:        aws.String(want.ContentType),
		CacheControl:       aws.String(want.CacheControl),
		ContentDisposition: aws.String(want.ContentDisposition),
		ContentEncoding:    aws.String(want.ContentEncoding),
		ContentLanguage:    aws.String(want.ContentLanguage),
		Expires:            aws.Time(expires),
	})
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	head := headObject(ctx, t, cli, bucket, key)
	if diff := cmp.Diff(want, headersFromHead(head)); diff != "" {
		t.Errorf("HeadObject headers mismatch (-want +got):\n%s", diff)
	}

	get, err := cli.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	bodyOf(t, get)
	if diff := cmp.Diff(want, headersFromGet(get)); diff != "" {
		t.Errorf("GetObject headers mismatch (-want +got):\n%s", diff)
	}
}

func TestObjectUserMetadataLimit(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)

	tests := []struct {
		name     string
		valueLen int
		wantCode string
	}{
		{name: "1000 bytes of metadata", valueLen: 1000},
		{name: "2100 bytes of metadata", valueLen: 2100, wantCode: "MetadataTooLarge"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := "limit-" + strings.ReplaceAll(tc.name, " ", "-")
			_, err := cli.PutObject(ctx, &s3.PutObjectInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				Body:     bytes.NewReader([]byte("m")),
				Metadata: map[string]string{"big": strings.Repeat("v", tc.valueLen)},
			})
			if tc.wantCode != "" {
				requireAPIError(t, err, http.StatusBadRequest, tc.wantCode)
				return
			}
			if err != nil {
				t.Fatalf("PutObject: %v", err)
			}
			head := headObject(ctx, t, cli, bucket, key)
			if got := len(head.Metadata["big"]); got != tc.valueLen {
				t.Errorf("stored metadata length = %d, want %d", got, tc.valueLen)
			}
		})
	}
}

// tagsOf converts a tag set into a map for comparison.
func tagsOf(tags []types.Tag) map[string]string {
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		m[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return m
}

func TestObjectTagging(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	const key = "tagged"
	putObject(ctx, t, cli, bucket, key, []byte("t"))

	setTags := func(tags map[string]string) {
		t.Helper()
		set := make([]types.Tag, 0, len(tags))
		for k, v := range tags {
			set = append(set, types.Tag{Key: aws.String(k), Value: aws.String(v)})
		}
		_, err := cli.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
			Bucket:  aws.String(bucket),
			Key:     aws.String(key),
			Tagging: &types.Tagging{TagSet: set},
		})
		if err != nil {
			t.Fatalf("PutObjectTagging: %v", err)
		}
	}
	getTags := func() map[string]string {
		t.Helper()
		out, err := cli.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		if err != nil {
			t.Fatalf("GetObjectTagging: %v", err)
		}
		return tagsOf(out.TagSet)
	}

	setTags(map[string]string{"env": "it", "team": "storage"})
	if diff := cmp.Diff(map[string]string{"env": "it", "team": "storage"}, getTags()); diff != "" {
		t.Errorf("tags after put mismatch (-want +got):\n%s", diff)
	}

	setTags(map[string]string{"env": "replaced"})
	if diff := cmp.Diff(map[string]string{"env": "replaced"}, getTags()); diff != "" {
		t.Errorf("tags after replace mismatch (-want +got):\n%s", diff)
	}

	if _, err := cli.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{Bucket: aws.String(bucket), Key: aws.String(key)}); err != nil {
		t.Fatalf("DeleteObjectTagging: %v", err)
	}
	if got := getTags(); len(got) != 0 {
		t.Errorf("tags after delete = %v, want none", got)
	}
}

func TestObjectTaggingMissingKey(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)

	_, err := cli.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String("absent"),
		Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("a"), Value: aws.String("b")}}},
	})
	requireAPIError(t, err, http.StatusNotFound, "NoSuchKey")
}
