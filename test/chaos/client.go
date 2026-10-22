package chaos

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// s3Region is the signing region. The local cluster ignores its value.
const s3Region = "us-east-1"

// maxIdleConnsPerHost keeps connections to each node open for the workers.
const maxIdleConnsPerHost = 16

// ObjectClient reads objects from the store under test.
type ObjectClient interface {
	Get(ctx context.Context, key string) (body []byte, err error)
}

// ObjectWriter stores objects in the store under test.
type ObjectWriter interface {
	Put(ctx context.Context, key string, body []byte) error
}

// S3Client talks to one endpoint of the cluster. Each call issues exactly one
// HTTP request. SDK retries are disabled so that a classified outcome matches
// one server response. The client does not use proxies from the environment.
type S3Client struct {
	api       *s3.Client
	bucket    string
	timeout   time.Duration
	transport *http.Transport
}

// NewS3Client returns a client for endpoint, using path-style addressing and
// static credentials. timeout bounds each request.
func NewS3Client(endpoint, bucket, accessKey, secretKey string, timeout time.Duration) (*S3Client, error) {
	if endpoint == "" || bucket == "" || timeout <= 0 {
		return nil, errors.New("chaos: endpoint, bucket and a positive timeout are required")
	}
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: timeout}).DialContext,
		ResponseHeaderTimeout: timeout,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       30 * time.Second,
	}
	cfg := aws.Config{
		Region:                     s3Region,
		Credentials:                credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:                 &http.Client{Transport: transport},
		RetryMaxAttempts:           1,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	api := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return &S3Client{api: api, bucket: bucket, timeout: timeout, transport: transport}, nil
}

// Put stores body under key. A nil error means the server answered 200.
func (c *S3Client) Put(ctx context.Context, key string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, err := c.api.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))),
	})
	if err != nil {
		return fmt.Errorf("put %s: %w", key, err)
	}
	return nil
}

// Get returns the full body stored under key.
func (c *S3Client) Get(ctx context.Context, key string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	out, err := c.api.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", key, err)
	}
	defer out.Body.Close()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", key, err)
	}
	return body, nil
}

// EnsureBucket creates the bucket unless it already exists.
func (c *S3Client) EnsureBucket(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	_, err := c.api.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(c.bucket)})
	if err == nil {
		return nil
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "BucketAlreadyOwnedByYou", "BucketAlreadyExists":
			return nil
		}
	}
	return fmt.Errorf("create bucket %s: %w", c.bucket, err)
}

// Close releases idle connections.
func (c *S3Client) Close() error {
	c.transport.CloseIdleConnections()
	return nil
}

// isNoSuchKey reports whether err is the S3 NoSuchKey error, which is the
// response to a GET of an absent key. When the error carries an HTTP status,
// the status must be 404 as well.
func isNoSuchKey(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchKey" {
		return false
	}
	var sc statusCarrier
	if errors.As(err, &sc) {
		return sc.HTTPStatusCode() == http.StatusNotFound
	}
	return true
}

// roundRobin spreads reads over several backends in turn.
type roundRobin struct {
	backends []ObjectClient
	next     atomic.Uint64
}

// RoundRobin returns an ObjectClient that sends each Get to the next backend.
// Verification uses it so that every node is asked for the objects.
func RoundRobin(backends ...ObjectClient) (ObjectClient, error) {
	if len(backends) == 0 {
		return nil, errors.New("chaos: round robin needs at least one backend")
	}
	return &roundRobin{backends: append([]ObjectClient(nil), backends...)}, nil
}

// Get forwards the call to the next backend in order.
func (r *roundRobin) Get(ctx context.Context, key string) ([]byte, error) {
	i := (r.next.Add(1) - 1) % uint64(len(r.backends))
	return r.backends[i].Get(ctx, key)
}
