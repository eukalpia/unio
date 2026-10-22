package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
)

// NewMultipartUpload implements object.Layer. The upload id is random, so it
// cannot be guessed to reach another upload. The conditions of opts are not
// stored: they apply to the completion only.
func (e *Engine) NewMultipartUpload(ctx context.Context, bucket, key string, opts object.PutOptions) (string, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return "", err
	}
	if err := validatePutOptions(opts); err != nil {
		return "", err
	}
	if err := checkCtx(ctx); err != nil {
		return "", err
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	stored := opts
	stored.Conditions = object.Conditions{}
	stored.UserMetadata = cloneMap(opts.UserMetadata)
	err = e.store.CreateUpload(ctx, meta.UploadRecord{
		Bucket:    bucket,
		Key:       key,
		UploadID:  id,
		Initiated: e.now(),
		Options:   stored,
	})
	if err != nil {
		return "", err
	}
	return id, nil
}
