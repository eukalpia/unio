package mirror

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// Endpoint describes one S3-compatible store.
type Endpoint struct {
	// URL is the base URL, for example https://minio.example:9000. It must
	// not embed credentials.
	URL       string
	Region    string
	AccessKey string
	SecretKey string
}

// maxSinglePut is the largest object sent in one PUT. Larger objects go in
// parts; S3 refuses a single PUT above 5 GiB.
const maxSinglePut = 5 << 30

// partSize is the part size of a multipart copy.
const partSize = 64 << 20

type s3Store struct {
	c *s3.Client
}

// NewS3 returns a Store for ep, with path-style addressing.
func NewS3(ep Endpoint) (Store, error) {
	u, err := url.Parse(ep.URL)
	// url.Parse errors quote the input, which might hold credentials.
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("endpoint must be an http or https URL")
	}
	if u.User != nil {
		return nil, errors.New("endpoint must not embed credentials; use the environment variables")
	}
	if ep.AccessKey == "" || ep.SecretKey == "" {
		return nil, errors.New("access key and secret key must both be set")
	}
	region := ep.Region
	if region == "" {
		region = "us-east-1"
	}
	cfg := aws.Config{
		Region:                     region,
		Credentials:                credentials.NewStaticCredentialsProvider(ep.AccessKey, ep.SecretKey, ""),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	c := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimRight(ep.URL, "/"))
		o.UsePathStyle = true
	})
	return &s3Store{c: c}, nil
}

func (s *s3Store) ListBuckets(ctx context.Context) ([]string, error) {
	out, err := s.c.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Buckets))
	for _, b := range out.Buckets {
		names = append(names, aws.ToString(b.Name))
	}
	return names, nil
}

func (s *s3Store) EnsureBucket(ctx context.Context, bucket string) error {
	if _, err := s.c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err == nil {
		return nil
	}
	_, err := s.c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	if code(err) == "BucketAlreadyOwnedByYou" {
		return nil
	}
	return err
}

func (s *s3Store) List(ctx context.Context, bucket string, fn func(Object) error) error {
	p := s3.NewListObjectsV2Paginator(s.c, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, o := range page.Contents {
			if err := fn(Object{Key: aws.ToString(o.Key), Size: aws.ToInt64(o.Size), ETag: unquote(aws.ToString(o.ETag))}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *s3Store) Head(ctx context.Context, bucket, key string) (Object, bool, error) {
	out, err := s.c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) || code(err) == "NoSuchKey" || code(err) == "NoSuchBucket" || code(err) == "NotFound" {
			return Object{}, false, nil
		}
		return Object{}, false, err
	}
	return Object{
		Key: key, Size: aws.ToInt64(out.ContentLength), ETag: unquote(aws.ToString(out.ETag)),
		ContentType: aws.ToString(out.ContentType), ContentDisposition: aws.ToString(out.ContentDisposition),
		ContentEncoding: aws.ToString(out.ContentEncoding), ContentLanguage: aws.ToString(out.ContentLanguage),
		CacheControl: aws.ToString(out.CacheControl), Metadata: out.Metadata,
	}, true, nil
}

func (s *s3Store) Get(ctx context.Context, bucket, key string) (io.ReadCloser, Object, error) {
	out, err := s.c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, Object{}, err
	}
	return out.Body, Object{
		Key: key, Size: aws.ToInt64(out.ContentLength), ETag: unquote(aws.ToString(out.ETag)),
		ContentType: aws.ToString(out.ContentType), ContentDisposition: aws.ToString(out.ContentDisposition),
		ContentEncoding: aws.ToString(out.ContentEncoding), ContentLanguage: aws.ToString(out.ContentLanguage),
		CacheControl: aws.ToString(out.CacheControl), Metadata: out.Metadata,
	}, nil
}

func (s *s3Store) Put(ctx context.Context, bucket string, o Object, body io.Reader, contentMD5 string) error {
	if o.Size > maxSinglePut {
		return s.putParts(ctx, bucket, o, body)
	}
	in := &s3.PutObjectInput{
		Bucket: aws.String(bucket), Key: aws.String(o.Key), Body: body, ContentLength: aws.Int64(o.Size),
		ContentType: nonEmpty(o.ContentType), ContentDisposition: nonEmpty(o.ContentDisposition),
		ContentEncoding: nonEmpty(o.ContentEncoding), ContentLanguage: nonEmpty(o.ContentLanguage),
		CacheControl: nonEmpty(o.CacheControl), Metadata: o.Metadata, ContentMD5: nonEmpty(contentMD5),
	}
	// The body is a stream from the source and cannot be read twice, so it
	// is not hashed for the signature. Its integrity is checked by
	// Content-MD5 when the source ETag allows it, and by the MD5 that Run
	// computes while copying.
	_, err := s.c.PutObject(ctx, in, unsignedPayload)
	return err
}

// putParts uploads o in parts of partSize. A failure aborts the upload so no
// parts are left behind.
func (s *s3Store) putParts(ctx context.Context, bucket string, o Object, body io.Reader) error {
	up, err := s.c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(o.Key),
		ContentType: nonEmpty(o.ContentType), ContentDisposition: nonEmpty(o.ContentDisposition),
		ContentEncoding: nonEmpty(o.ContentEncoding), ContentLanguage: nonEmpty(o.ContentLanguage),
		CacheControl: nonEmpty(o.CacheControl), Metadata: o.Metadata,
	})
	if err != nil {
		return err
	}
	abort := func(cause error) error {
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = s.c.AbortMultipartUpload(actx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(o.Key), UploadId: up.UploadId})
		return cause
	}
	var parts []types.CompletedPart
	buf := make([]byte, partSize)
	for n := int32(1); ; n++ {
		read, rerr := io.ReadFull(body, buf)
		if read > 0 {
			out, err := s.c.UploadPart(ctx, &s3.UploadPartInput{
				Bucket: aws.String(bucket), Key: aws.String(o.Key), UploadId: up.UploadId,
				PartNumber: aws.Int32(n), Body: bytes.NewReader(buf[:read]), ContentLength: aws.Int64(int64(read)),
			})
			if err != nil {
				return abort(err)
			}
			parts = append(parts, types.CompletedPart{ETag: out.ETag, PartNumber: aws.Int32(n)})
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return abort(rerr)
		}
	}
	_, err = s.c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(o.Key), UploadId: up.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		return abort(err)
	}
	return nil
}

func (s *s3Store) GetPolicy(ctx context.Context, bucket string) ([]byte, error) {
	out, err := s.c.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
	if err != nil {
		if c := code(err); c == "NoSuchBucketPolicy" || c == "NoSuchBucket" {
			return nil, nil
		}
		return nil, err
	}
	return []byte(aws.ToString(out.Policy)), nil
}

func (s *s3Store) PutPolicy(ctx context.Context, bucket string, doc []byte) error {
	_, err := s.c.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucket), Policy: aws.String(string(doc))})
	return err
}

// unsignedPayload signs the request headers but not the streamed body.
func unsignedPayload(o *s3.Options) {
	o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
}

func code(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

func unquote(etag string) string { return strings.Trim(etag, `"`) }

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return aws.String(s)
}

// String hides the client so that a Store never prints credentials.
func (s *s3Store) String() string { return fmt.Sprintf("mirror.s3Store{%p}", s) }
