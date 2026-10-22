package metatest

import (
	"context"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// CreateBucket implements meta.Store. The name is validated first, then the
// bucket is created with the next never-reused BucketID.
func (f *Fake) CreateBucket(_ context.Context, name string, opts object.MakeBucketOptions) (object.BucketInfo, error) {
	if err := object.ValidateBucketName(name); err != nil {
		return object.BucketInfo{}, err
	}
	if err := validVersioning(opts.Versioning); err != nil {
		return object.BucketInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if b, ok := f.buckets[name]; ok {
		if b.info.Owner == opts.Owner {
			return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyOwnedByYou, "bucket %q is already owned by you", name)
		}
		return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyExists, "bucket %q already exists", name)
	}
	f.lastBucketID++
	info := object.BucketInfo{
		Name:       name,
		ID:         f.lastBucketID,
		Created:    time.Now(),
		Owner:      opts.Owner,
		Versioning: opts.Versioning,
	}
	f.buckets[name] = newBucketState(info)
	return info, nil
}

// GetBucket implements meta.Store.
func (f *Fake) GetBucket(_ context.Context, name string) (object.BucketInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(name)
	if err != nil {
		return object.BucketInfo{}, err
	}
	return b.info, nil
}

// ListBuckets implements meta.Store. Buckets are ordered by name.
func (f *Fake) ListBuckets(_ context.Context) ([]object.BucketInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := sortedKeys(f.buckets)
	out := make([]object.BucketInfo, 0, len(names))
	for _, name := range names {
		out = append(out, f.buckets[name].info)
	}
	return out, nil
}

// DeleteBucket implements meta.Store. A bucket with any object version,
// delete marker or in-progress upload is not empty.
func (f *Fake) DeleteBucket(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(name)
	if err != nil {
		return err
	}
	if len(b.objects) > 0 || len(b.uploads) > 0 {
		return s3err.Errorf(s3err.BucketNotEmpty, "bucket %q is not empty", name)
	}
	delete(f.buckets, name)
	return nil
}

// SetBucketVersioning implements meta.Store.
func (f *Fake) SetBucketVersioning(_ context.Context, name string, state object.VersioningState) error {
	if err := validVersioning(state); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(name)
	if err != nil {
		return err
	}
	b.info.Versioning = state
	return nil
}
