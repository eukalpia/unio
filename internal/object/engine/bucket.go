package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// MakeBucket implements object.Layer.
func (e *Engine) MakeBucket(ctx context.Context, bucket string, opts object.MakeBucketOptions) (object.BucketInfo, error) {
	if err := object.ValidateBucketName(bucket); err != nil {
		return object.BucketInfo{}, err
	}
	if opts.Versioning > object.VersioningSuspended {
		return object.BucketInfo{}, s3err.Errorf(s3err.InvalidArgument, "unknown versioning state")
	}
	return e.store.CreateBucket(ctx, bucket, opts)
}

// GetBucketInfo implements object.Layer.
func (e *Engine) GetBucketInfo(ctx context.Context, bucket string) (object.BucketInfo, error) {
	if err := object.ValidateBucketName(bucket); err != nil {
		return object.BucketInfo{}, err
	}
	return e.store.GetBucket(ctx, bucket)
}

// ListBuckets implements object.Layer.
func (e *Engine) ListBuckets(ctx context.Context) ([]object.BucketInfo, error) {
	return e.store.ListBuckets(ctx)
}

// DeleteBucket implements object.Layer. With Force, the bucket's in-progress
// uploads are aborted and its objects are deleted first. Callers must be
// authorised to force deletion.
//
// Force removes the current version of every key. Old versions and delete
// markers of a versioned bucket are not listed by the store, so such a bucket
// still fails with BucketNotEmpty.
func (e *Engine) DeleteBucket(ctx context.Context, bucket string, opts object.DeleteBucketOptions) error {
	if err := object.ValidateBucketName(bucket); err != nil {
		return err
	}
	if opts.Force {
		if err := e.emptyBucket(ctx, bucket); err != nil {
			return err
		}
	}
	return e.store.DeleteBucket(ctx, bucket)
}

// emptyBucket aborts every upload of bucket and deletes every current object,
// one page at a time.
func (e *Engine) emptyBucket(ctx context.Context, bucket string) error {
	if _, err := e.store.GetBucket(ctx, bucket); err != nil {
		return err
	}
	var keyMarker, idMarker string
	for {
		page, err := e.store.ListUploads(ctx, bucket, object.ListUploadsOptions{
			KeyMarker:      keyMarker,
			UploadIDMarker: idMarker,
			MaxUploads:     object.MaxListKeys,
		})
		if err != nil {
			return err
		}
		for _, u := range page.Uploads {
			parts, err := e.store.AbortUpload(ctx, bucket, u.Key, u.UploadID)
			if err != nil && !s3err.Is(err, s3err.NoSuchUpload) {
				return err
			}
			e.reclaimParts(parts)
		}
		if !page.IsTruncated {
			break
		}
		keyMarker, idMarker = page.NextKeyMarker, page.NextUploadIDMarker
	}

	var after string
	for {
		page, err := e.store.ListObjects(ctx, bucket, object.ListOptions{After: after, MaxKeys: object.MaxListKeys})
		if err != nil {
			return err
		}
		for _, rec := range page.Objects {
			_, garbage, err := e.store.DeleteObject(ctx, bucket, rec.Key, "", object.Conditions{})
			if err != nil && !s3err.Is(err, s3err.NoSuchKey) {
				return err
			}
			e.reclaim(garbage)
		}
		if !page.IsTruncated {
			return nil
		}
		after = page.NextAfter
	}
}
