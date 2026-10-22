package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// GetObject implements object.Layer. The returned reader verifies every block
// it serves (I2). For PartNumber reads, Info.Size and the window refer to the
// selected part; Info.Parts gives the number of parts of the object.
func (e *Engine) GetObject(ctx context.Context, bucket, key string, opts object.GetOptions) (*object.Reader, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return nil, err
	}
	if err := checkPartNumberOpt(opts.PartNumber); err != nil {
		return nil, err
	}
	rec, err := e.lookupRead(ctx, bucket, key, opts.VersionID)
	if err != nil {
		return nil, err
	}
	info := infoOf(rec, opts.VersionID == "")
	if err := evaluateRead(opts.Conditions, info); err != nil {
		return nil, err
	}
	c, err := selectPart(rec, contentOf(rec), opts.PartNumber)
	if err != nil {
		return nil, err
	}
	off, length, err := c.window(opts.Range)
	if err != nil {
		return nil, err
	}
	rc, err := e.open(ctx, c, off, length)
	if err != nil {
		return nil, err
	}
	info.Size = c.size
	return &object.Reader{ReadCloser: rc, Info: info, Offset: off, Length: length}, nil
}

// lookupRead fetches the version a read addresses. A latest delete marker is
// reported as NoSuchKey, with the marker in the error's cause. An explicit
// request for a delete marker's version is MethodNotAllowed, as in S3.
func (e *Engine) lookupRead(ctx context.Context, bucket, key, versionID string) (meta.ObjectRecord, error) {
	storeID, err := e.nullVersionOf(ctx, bucket, versionID)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	rec, err := e.store.GetObject(ctx, bucket, key, storeID)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	if !rec.DeleteMarker {
		return rec, nil
	}
	if versionID != "" {
		return meta.ObjectRecord{}, s3err.Errorf(s3err.MethodNotAllowed, "the requested version is a delete marker")
	}
	return meta.ObjectRecord{}, noSuchKeyForMarker(infoOf(rec, true))
}
