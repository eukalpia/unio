package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// AbortMultipartUpload implements object.Layer. The parts of the upload are
// queued for deletion once the store has removed the upload.
func (e *Engine) AbortMultipartUpload(ctx context.Context, bucket, key, uploadID string) error {
	if err := validateBucketKey(bucket, key); err != nil {
		return err
	}
	if err := validateUploadID(uploadID); err != nil {
		return err
	}
	if err := checkCtx(ctx); err != nil {
		return err
	}
	parts, err := e.store.AbortUpload(ctx, bucket, key, uploadID)
	if err != nil {
		return err
	}
	e.reclaimParts(parts)
	return nil
}

// ListParts implements object.Layer.
func (e *Engine) ListParts(ctx context.Context, bucket, key, uploadID string, opts object.ListPartsOptions) (object.ListPartsResult, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.ListPartsResult{}, err
	}
	if err := validateUploadID(uploadID); err != nil {
		return object.ListPartsResult{}, err
	}
	if opts.PartNumberMarker < 0 {
		return object.ListPartsResult{}, s3err.Errorf(s3err.InvalidArgument, "part number marker must not be negative")
	}
	limit, err := resolveLimit(opts.MaxParts)
	if err != nil {
		return object.ListPartsResult{}, err
	}
	page, res, err := e.store.ListParts(ctx, bucket, key, uploadID, object.ListPartsOptions{
		PartNumberMarker: opts.PartNumberMarker,
		MaxParts:         limit,
	})
	if err != nil {
		return object.ListPartsResult{}, err
	}
	out := object.ListPartsResult{
		Parts:                make([]object.PartInfo, 0, len(page)),
		IsTruncated:          res.IsTruncated,
		NextPartNumberMarker: res.NextPartNumberMarker,
		ChecksumAlgorithm:    res.ChecksumAlgorithm,
	}
	for _, p := range page {
		out.Parts = append(out.Parts, partInfo(p))
	}
	return out, nil
}

// ListMultipartUploads implements object.Layer.
func (e *Engine) ListMultipartUploads(ctx context.Context, bucket string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	if err := object.ValidateBucketName(bucket); err != nil {
		return object.ListUploadsResult{}, err
	}
	limit, err := resolveLimit(opts.MaxUploads)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	opts.MaxUploads = limit
	return e.store.ListUploads(ctx, bucket, opts)
}
