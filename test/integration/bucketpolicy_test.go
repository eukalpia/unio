package integration

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// TestBucketPolicyPublicPrefixes opens a few prefixes of a bucket to anonymous
// reads, the way a web application publishes some files and keeps the rest
// private, and checks that nothing else is reachable without a signature.
func TestBucketPolicyPublicPrefixes(t *testing.T) {
	t.Parallel()
	ctx, cli := setup(t)
	bucket := newBucket(ctx, t, cli)
	base := strings.TrimSuffix(aws.ToString(cli.Options().BaseEndpoint), "/") + "/" + bucket + "/"
	for _, key := range []string{"public/logo.png", "users/7/photo.jpg", "users/7/passport.jpg", "private/report.pdf"} {
		putObject(ctx, t, cli, bucket, key, []byte("body of "+key))
	}
	anonymous := func(method, key string) int {
		return sendRaw(ctx, t, method, base+key, http.Header{}, nil).status
	}
	if got := anonymous("GET", "public/logo.png"); got != http.StatusForbidden {
		t.Fatalf("anonymous GET before any policy: %d, want 403", got)
	}

	doc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"AWS":["*"]},"Action":["s3:GetObject"],` +
		`"Resource":["arn:aws:s3:::` + bucket + `/public/*","arn:aws:s3:::` + bucket + `/users/*/photo.*"]}]}`
	if _, err := cli.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucket), Policy: aws.String(doc)}); err != nil {
		t.Fatalf("PutBucketPolicy: %v", err)
	}
	got, err := cli.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if err != nil || aws.ToString(got.Policy) != doc {
		t.Fatalf("GetBucketPolicy: %v, document changed: %v", err, aws.ToString(got.Policy) != doc)
	}

	cases := []struct {
		method, key string
		want        int
	}{
		{"GET", "public/logo.png", http.StatusOK},
		{"HEAD", "public/logo.png", http.StatusOK},
		{"GET", "users/7/photo.jpg", http.StatusOK},
		{"GET", "users/7/passport.jpg", http.StatusForbidden},
		{"GET", "private/report.pdf", http.StatusForbidden},
		{"PUT", "public/new.png", http.StatusForbidden},
		{"DELETE", "public/logo.png", http.StatusForbidden},
		{"GET", "?list-type=2", http.StatusForbidden},
		{"GET", "?policy", http.StatusForbidden},
	}
	for _, c := range cases {
		if got := anonymous(c.method, c.key); got != c.want {
			t.Errorf("anonymous %s %s: %d, want %d", c.method, c.key, got, c.want)
		}
	}

	// A document the server cannot honour is refused and the old one stays.
	bad := strings.Replace(doc, `"Resource":["arn:aws:s3:::`+bucket, `"Resource":["arn:aws:s3:::other-bucket`, 1)
	_, err = cli.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucket), Policy: aws.String(bad)})
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "MalformedPolicy" {
		t.Fatalf("policy naming another bucket: %v, want MalformedPolicy", err)
	}
	if got := anonymous("GET", "public/logo.png"); got != http.StatusOK {
		t.Fatalf("refused document replaced the stored one: %d", got)
	}

	if _, err := cli.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("DeleteBucketPolicy: %v", err)
	}
	if got := anonymous("GET", "public/logo.png"); got != http.StatusForbidden {
		t.Fatalf("anonymous GET after DeleteBucketPolicy: %d, want 403", got)
	}
	_, err = cli.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchBucketPolicy" {
		t.Fatalf("GetBucketPolicy after delete: %v, want NoSuchBucketPolicy", err)
	}
}
