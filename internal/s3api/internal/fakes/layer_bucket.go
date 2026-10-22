package fakes

import (
	"context"
	"maps"
	"slices"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// MakeBucket creates a bucket owned by opts.Owner, or by DefaultOwner when
// it is empty. Creating a bucket that exists returns BucketAlreadyOwnedByYou
// for the same owner and BucketAlreadyExists otherwise. Object versioning is
// not implemented, so only unversioned buckets can be created.
func (l *Layer) MakeBucket(ctx context.Context, bucketName string, opts object.MakeBucketOptions) (object.BucketInfo, error) {
	if err := ctx.Err(); err != nil {
		return object.BucketInfo{}, err
	}
	if err := object.ValidateBucketName(bucketName); err != nil {
		return object.BucketInfo{}, err
	}
	if opts.Versioning != object.VersioningUnversioned {
		return object.BucketInfo{}, s3err.Errorf(s3err.NotImplemented, "bucket versioning is not supported by the fake layer")
	}
	owner := opts.Owner
	if owner == "" {
		owner = DefaultOwner
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.buckets[bucketName]; ok {
		if existing.info.Owner == owner {
			return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyOwnedByYou, "bucket %q already exists and is owned by you", bucketName)
		}
		return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyExists, "bucket %q already exists", bucketName)
	}
	if l.buckets == nil {
		l.buckets = make(map[string]*bucket)
	}
	l.nextID++
	b := &bucket{
		info: object.BucketInfo{
			Name:    bucketName,
			ID:      ids.BucketID(l.nextID),
			Created: now(),
			Owner:   owner,
		},
		objects: make(map[string]*storedObject),
		uploads: make(map[string]*upload),
	}
	l.buckets[bucketName] = b
	return b.info, nil
}

// GetBucketInfo returns the description of a bucket, or NoSuchBucket.
func (l *Layer) GetBucketInfo(ctx context.Context, bucketName string) (object.BucketInfo, error) {
	if err := ctx.Err(); err != nil {
		return object.BucketInfo{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.BucketInfo{}, err
	}
	return b.info, nil
}

// ListBuckets returns every bucket sorted by name.
func (l *Layer) ListBuckets(ctx context.Context) ([]object.BucketInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	names := slices.Sorted(maps.Keys(l.buckets))
	out := make([]object.BucketInfo, 0, len(names))
	for _, name := range names {
		out = append(out, l.buckets[name].info)
	}
	return out, nil
}

// DeleteBucket removes a bucket. A bucket that still holds objects or
// uploads is rejected with BucketNotEmpty unless opts.Force is set, in which
// case everything in it is removed.
func (l *Layer) DeleteBucket(ctx context.Context, bucketName string, opts object.DeleteBucketOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return err
	}
	if !opts.Force && (len(b.objects) > 0 || len(b.uploads) > 0) {
		return s3err.Errorf(s3err.BucketNotEmpty, "bucket %q is not empty", bucketName)
	}
	delete(l.buckets, bucketName)
	return nil
}
