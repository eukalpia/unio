package engine

import (
	"context"
	"fmt"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// PutObjectTags implements object.Layer. The tag set is checked with the rules
// of the object layer before the store is called. The store replaces the tags
// of the version in one atomic step and creates no version, so the data, the
// ETag and the modification time stay as they are.
func (e *Engine) PutObjectTags(ctx context.Context, bucket, key string, opts object.TagOptions) (object.Info, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.Info{}, err
	}
	if err := object.ValidateTags(opts.Tags); err != nil {
		return object.Info{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.Info{}, err
	}
	rec, err := e.store.UpdateTags(ctx, bucket, key, opts.VersionID, object.EncodeTags(opts.Tags))
	if err != nil {
		return object.Info{}, err
	}
	return infoOf(rec, opts.VersionID == ""), nil
}

// GetObjectTags implements object.Layer. A latest delete marker reads as
// NoSuchKey and an explicit marker as MethodNotAllowed, as for object reads.
func (e *Engine) GetObjectTags(ctx context.Context, bucket, key, versionID string) (object.Info, object.TagSet, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.Info{}, nil, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.Info{}, nil, err
	}
	rec, err := e.lookupRead(ctx, bucket, key, versionID)
	if err != nil {
		return object.Info{}, nil, err
	}
	tags, err := object.ParseTags(rec.Tags)
	if err != nil {
		// The stored form is written by this package, so a failure here is a
		// storage fault. It must not reach the client as a tag error.
		return object.Info{}, nil, s3err.Wrap(s3err.InternalError, fmt.Errorf("engine: stored tags of %s/%s: %w", bucket, key, err))
	}
	return infoOf(rec, versionID == ""), tags, nil
}

// DeleteObjectTags implements object.Layer. It is PutObjectTags with an empty
// tag set.
func (e *Engine) DeleteObjectTags(ctx context.Context, bucket, key, versionID string) (object.Info, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.Info{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.Info{}, err
	}
	rec, err := e.store.UpdateTags(ctx, bucket, key, versionID, "")
	if err != nil {
		return object.Info{}, err
	}
	return infoOf(rec, versionID == ""), nil
}
