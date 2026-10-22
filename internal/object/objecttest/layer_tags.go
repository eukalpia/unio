package objecttest

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// PutObjectTags implements object.Layer. The layer has no versions, so
// VersionID must be empty.
func (l *Layer) PutObjectTags(_ context.Context, bucketName, key string, opts object.TagOptions) (object.Info, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.Info{}, err
	}
	if err := object.ValidateTags(opts.Tags); err != nil {
		return object.Info{}, err
	}
	if opts.VersionID != "" {
		return object.Info{}, s3err.Errorf(s3err.NoSuchVersion, "versions are not supported")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.Info{}, err
	}
	info, ok := b.objects[key]
	if !ok {
		return object.Info{}, s3err.Errorf(s3err.NoSuchKey, "no such key")
	}
	info.Tags = object.EncodeTags(opts.Tags)
	return copyInfo(*info), nil
}

// GetObjectTags implements object.Layer.
func (l *Layer) GetObjectTags(_ context.Context, bucketName, key, versionID string) (object.Info, object.TagSet, error) {
	if err := checkBucketKey(bucketName, key); err != nil {
		return object.Info{}, nil, err
	}
	if versionID != "" {
		return object.Info{}, nil, s3err.Errorf(s3err.NoSuchVersion, "versions are not supported")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.Info{}, nil, err
	}
	info, ok := b.objects[key]
	if !ok {
		return object.Info{}, nil, s3err.Errorf(s3err.NoSuchKey, "no such key")
	}
	tags, err := object.ParseTags(info.Tags)
	if err != nil {
		return object.Info{}, nil, err
	}
	return copyInfo(*info), tags, nil
}

// DeleteObjectTags implements object.Layer. It is PutObjectTags with an empty
// tag set.
func (l *Layer) DeleteObjectTags(ctx context.Context, bucketName, key, versionID string) (object.Info, error) {
	return l.PutObjectTags(ctx, bucketName, key, object.TagOptions{VersionID: versionID})
}
