package object

import "context"

// Layer is the storage API used by the S3 front end.
//
// All methods validate bucket names, keys and limits and return
// *s3err.Error values for client errors. Implementations are safe for
// concurrent use.
type Layer interface {
	MakeBucket(ctx context.Context, bucket string, opts MakeBucketOptions) (BucketInfo, error)
	GetBucketInfo(ctx context.Context, bucket string) (BucketInfo, error)
	ListBuckets(ctx context.Context) ([]BucketInfo, error)
	DeleteBucket(ctx context.Context, bucket string, opts DeleteBucketOptions) error

	// PutObject stores body as the new current version of bucket/key. The
	// version becomes visible only after body was read to io.EOF and the data
	// is durable on a write quorum (I1, I4).
	PutObject(ctx context.Context, bucket, key string, body Body, opts PutOptions) (Info, error)
	// GetObject evaluates opts.Conditions and opts.Range and returns a reader
	// that verifies every block it serves (I2).
	GetObject(ctx context.Context, bucket, key string, opts GetOptions) (*Reader, error)
	HeadObject(ctx context.Context, bucket, key string, opts GetOptions) (Info, error)
	DeleteObject(ctx context.Context, bucket, key string, opts DeleteOptions) (DeleteResult, error)
	// DeleteObjects deletes up to MaxDeleteObjects keys; per-key failures are
	// reported in the results, the error is for request-level failures.
	DeleteObjects(ctx context.Context, bucket string, objects []ToDelete) ([]DeleteResult, error)
	CopyObject(ctx context.Context, src Ref, dstBucket, dstKey string, opts CopyOptions) (Info, error)
	ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListResult, error)

	NewMultipartUpload(ctx context.Context, bucket, key string, opts PutOptions) (uploadID string, err error)
	PutObjectPart(ctx context.Context, bucket, key, uploadID string, partNumber int, body Body) (PartInfo, error)
	CopyObjectPart(ctx context.Context, src Ref, bucket, key, uploadID string, partNumber int, opts CopyOptions) (PartInfo, error)
	ListParts(ctx context.Context, bucket, key, uploadID string, opts ListPartsOptions) (ListPartsResult, error)
	// CompleteMultipartUpload atomically turns the listed parts into the
	// current version. opts carries destination conditions only.
	CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []CompletePart, opts PutOptions) (Info, error)
	AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error
	ListMultipartUploads(ctx context.Context, bucket string, opts ListUploadsOptions) (ListUploadsResult, error)
}
