package objstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// defaultRegion is the region that needs no location constraint on bucket
// creation.
const defaultRegion = "us-east-1"

// Credentials are the static keys used to sign requests. They are never
// printed: String and GoString redact them.
type Credentials struct {
	AccessKey string
	SecretKey string
}

// String implements fmt.Stringer without revealing the keys.
func (Credentials) String() string { return "objstore.Credentials{redacted}" }

// GoString implements fmt.GoStringer without revealing the keys.
func (Credentials) GoString() string { return "objstore.Credentials{redacted}" }

// Options configures the AWS-backed Store.
type Options struct {
	// Endpoint is the base URL of the S3 API, for example http://localhost:9000.
	// It must be http or https and must not carry credentials.
	Endpoint string
	// Region is the signing region.
	Region string
	// PathStyle selects path-style addressing, which S3-compatible servers
	// usually require.
	PathStyle bool
	// Credentials signs every request.
	Credentials Credentials
	// Concurrency is the number of parallel workers. The idle connection pool
	// is sized to it so connections are reused rather than re-dialled.
	Concurrency int
}

// awsStore implements Store with the AWS SDK for Go v2.
type awsStore struct {
	client *s3.Client
	region string
}

// NewAWS returns a Store that talks to opts.Endpoint. Retries are disabled so
// that every failure reaches the error counters, and checksums are calculated
// only when the API requires them, so the client does the same work against
// every endpoint.
func NewAWS(opts Options) (Store, error) {
	if err := validate(opts); err != nil {
		return nil, err
	}
	idle := max(opts.Concurrency, 1)
	httpClient := awshttp.NewBuildableClient().WithTransportOptions(func(t *http.Transport) {
		t.MaxIdleConns = idle
		t.MaxIdleConnsPerHost = idle
	})
	cfg := aws.Config{
		Region:                     opts.Region,
		Credentials:                credentials.NewStaticCredentialsProvider(opts.Credentials.AccessKey, opts.Credentials.SecretKey, ""),
		HTTPClient:                 httpClient,
		RetryMaxAttempts:           1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(opts.Endpoint)
		o.UsePathStyle = opts.PathStyle
	})
	return &awsStore{client: client, region: opts.Region}, nil
}

// validate checks the options without echoing any credential value.
func validate(opts Options) error {
	// url.Parse errors quote the whole input, which may embed credentials, so
	// the message is generic.
	u, err := url.Parse(opts.Endpoint)
	if err != nil {
		return errors.New("endpoint is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("endpoint has no host")
	}
	if u.User != nil {
		return errors.New("endpoint must not embed credentials; use the environment variables")
	}
	if opts.Region == "" {
		return errors.New("region is empty")
	}
	if opts.Credentials.AccessKey == "" || opts.Credentials.SecretKey == "" {
		return errors.New("access key and secret key must both be set")
	}
	return nil
}

// EnsureBucket creates bucket unless a HEAD request shows that it exists. An
// existing bucket owned by the caller is not an error.
func (s *awsStore) EnsureBucket(ctx context.Context, bucket string) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err == nil {
		return nil
	}
	in := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	if s.region != defaultRegion {
		in.CreateBucketConfiguration = &types.CreateBucketConfiguration{
			LocationConstraint: types.BucketLocationConstraint(s.region),
		}
	}
	_, err := s.client.CreateBucket(ctx, in)
	var owned *types.BucketAlreadyOwnedByYou
	if err == nil || errors.As(err, &owned) {
		return nil
	}
	return fmt.Errorf("create bucket %q: %w", bucket, err)
}

// PutObject uploads body under key.
func (s *awsStore) PutObject(ctx context.Context, bucket, key string, body []byte) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	})
	if err != nil {
		return fmt.Errorf("put %q: %w", key, err)
	}
	return nil
}

// GetObject downloads key, drains the body and returns the number of bytes
// read.
func (s *awsStore) GetObject(ctx context.Context, bucket, key string) (int64, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, fmt.Errorf("get %q: %w", key, err)
	}
	n, copyErr := io.Copy(io.Discard, out.Body)
	closeErr := out.Body.Close()
	if copyErr != nil {
		return n, fmt.Errorf("read %q: %w", key, copyErr)
	}
	if closeErr != nil {
		return n, fmt.Errorf("close %q: %w", key, closeErr)
	}
	return n, nil
}

// ListObjectsV2 fetches one page. The continuation token, when present, is
// sent back unchanged, so the server resumes exactly where the previous page
// stopped.
func (s *awsStore) ListObjectsV2(ctx context.Context, bucket string, req ListRequest) (ListPage, error) {
	in := &s3.ListObjectsV2Input{Bucket: aws.String(bucket)}
	if req.Prefix != "" {
		in.Prefix = aws.String(req.Prefix)
	}
	if req.Delimiter != "" {
		in.Delimiter = aws.String(req.Delimiter)
	}
	if req.MaxKeys > 0 {
		in.MaxKeys = aws.Int32(req.MaxKeys)
	}
	if req.ContinuationToken != "" {
		in.ContinuationToken = aws.String(req.ContinuationToken)
	}
	out, err := s.client.ListObjectsV2(ctx, in)
	if err != nil {
		return ListPage{}, fmt.Errorf("list %q: %w", req.Prefix, err)
	}
	return ListPage{
		Keys:           len(out.Contents),
		CommonPrefixes: len(out.CommonPrefixes),
		Truncated:      aws.ToBool(out.IsTruncated),
		NextToken:      aws.ToString(out.NextContinuationToken),
	}, nil
}
