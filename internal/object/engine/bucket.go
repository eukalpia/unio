package engine

import (
	"context"
	"slices"

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
	buckets, err := e.store.ListBuckets(ctx)
	if err != nil {
		return nil, err
	}
	return slices.Clone(buckets), nil
}

// DeleteBucket implements object.Layer.
//
// With Force, the bucket's in-progress uploads are aborted and its current
// objects are deleted before the bucket is removed. Callers must be authorised
// to force deletion. Forced deletion works only for unversioned buckets. The
// store lists current versions only, and a delete in a versioned or suspended
// bucket adds a delete marker instead of removing anything. So for those
// buckets, Force refuses with BucketNotEmpty, without changing anything, while
// current objects or uploads remain. Old versions and delete markers are not
// listed, so such a bucket also fails with BucketNotEmpty.
//
// For an unversioned bucket, a failure part way through leaves some objects
// deleted. The bucket still exists and the call can be repeated.
func (e *Engine) DeleteBucket(ctx context.Context, bucket string, opts object.DeleteBucketOptions) error {
	if err := object.ValidateBucketName(bucket); err != nil {
		return err
	}
	if opts.Force {
		info, err := e.store.GetBucket(ctx, bucket)
		if err != nil {
			return err
		}
		if info.Versioning == object.VersioningUnversioned {
			if err := e.emptyBucket(ctx, bucket); err != nil {
				return err
			}
		} else if err := e.checkEmptyVersioned(ctx, bucket); err != nil {
			return err
		}
	}
	return e.store.DeleteBucket(ctx, bucket)
}

// checkEmptyVersioned reports BucketNotEmpty when a versioned bucket still has
// current objects or uploads. It changes nothing.
func (e *Engine) checkEmptyVersioned(ctx context.Context, bucket string) error {
	objs, err := e.store.ListObjects(ctx, bucket, object.ListOptions{MaxKeys: 1})
	if err != nil {
		return err
	}
	if len(objs.Objects) > 0 || len(objs.CommonPrefixes) > 0 {
		return s3err.Errorf(s3err.BucketNotEmpty, "a versioned bucket must be empty to be force-deleted")
	}
	ups, err := e.store.ListUploads(ctx, bucket, object.ListUploadsOptions{MaxUploads: 1})
	if err != nil {
		return err
	}
	if len(ups.Uploads) > 0 || len(ups.CommonPrefixes) > 0 {
		return s3err.Errorf(s3err.BucketNotEmpty, "a versioned bucket must be empty to be force-deleted")
	}
	return nil
}

// emptyBucket aborts every upload of bucket and deletes every current object,
// one page at a time. It is used only for unversioned buckets.
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
