package fakes

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// PutObjectTags implements object.Layer. The fake layer has no versions, so
// VersionID must be empty. The stored object is replaced by a copy that
// carries the new tags and shares the data, so readers that hold the old
// object keep their view.
func (l *Layer) PutObjectTags(ctx context.Context, bucketName, key string, opts object.TagOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	if err := object.ValidateTags(opts.Tags); err != nil {
		return object.Info{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, obj, err := l.taggedLocked(bucketName, key, opts.VersionID)
	if err != nil {
		return object.Info{}, err
	}
	next := &storedObject{info: cloneInfo(obj.info), data: obj.data}
	next.info.Tags = object.EncodeTags(opts.Tags)
	b.objects[key] = next
	return cloneInfo(next.info), nil
}

// GetObjectTags implements object.Layer.
func (l *Layer) GetObjectTags(ctx context.Context, bucketName, key, versionID string) (object.Info, object.TagSet, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, obj, err := l.taggedLocked(bucketName, key, versionID)
	if err != nil {
		return object.Info{}, nil, err
	}
	tags, err := object.ParseTags(obj.info.Tags)
	if err != nil {
		return object.Info{}, nil, err
	}
	return cloneInfo(obj.info), tags, nil
}

// DeleteObjectTags implements object.Layer. It is PutObjectTags with an empty
// tag set.
func (l *Layer) DeleteObjectTags(ctx context.Context, bucketName, key, versionID string) (object.Info, error) {
	return l.PutObjectTags(ctx, bucketName, key, object.TagOptions{VersionID: versionID})
}

// taggedLocked resolves the object that a tagging request addresses: the
// current version of bucketName/key. A version id is always unknown here,
// because the layer keeps no older versions. l.mu must be held.
func (l *Layer) taggedLocked(bucketName, key, versionID string) (*bucket, *storedObject, error) {
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return nil, nil, err
	}
	if err := object.ValidateKey(key); err != nil {
		return nil, nil, err
	}
	obj, ok := b.objects[key]
	if !ok {
		return nil, nil, s3err.Errorf(s3err.NoSuchKey, "key %q does not exist", key)
	}
	if versionID != "" {
		return nil, nil, s3err.Errorf(s3err.NoSuchVersion, "version %q does not exist", versionID)
	}
	return b, obj, nil
}
