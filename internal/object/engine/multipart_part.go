package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// PutObjectPart implements object.Layer. Parts always use the erasure path,
// whatever their size. The upload is checked before any data is written, so
// a request for an unknown upload writes nothing. A part with the same number
// replaces the earlier one, whose data is then queued for deletion.
func (e *Engine) PutObjectPart(ctx context.Context, bucket, key, uploadID string, partNumber int, body object.Body) (object.PartInfo, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.PartInfo{}, err
	}
	if err := validatePartNumber(partNumber); err != nil {
		return object.PartInfo{}, err
	}
	if err := validateUploadID(uploadID); err != nil {
		return object.PartInfo{}, err
	}
	if err := validateSize(body.Size(), object.MaxPartSize, s3err.EntityTooLarge); err != nil {
		return object.PartInfo{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.PartInfo{}, err
	}
	if _, err := e.store.GetUpload(ctx, bucket, key, uploadID); err != nil {
		return object.PartInfo{}, err
	}
	set, err := e.sets.Place(ctx, bucket, key, body.Size())
	if err != nil {
		return object.PartInfo{}, wrapCause("placing data", err)
	}
	part, err := e.writePart(ctx, set, body, partNumber, e.now())
	if err != nil {
		return object.PartInfo{}, err
	}
	return e.storePart(ctx, bucket, key, uploadID, part)
}

// CopyObjectPart implements object.Layer. The source window is streamed into
// the part's erasure set, as CopyObject does for whole objects.
func (e *Engine) CopyObjectPart(ctx context.Context, src object.Ref, bucket, key, uploadID string, partNumber int, opts object.CopyOptions) (object.PartInfo, error) {
	if err := validateRef(src); err != nil {
		return object.PartInfo{}, err
	}
	if err := validateBucketKey(bucket, key); err != nil {
		return object.PartInfo{}, err
	}
	if err := validatePartNumber(partNumber); err != nil {
		return object.PartInfo{}, err
	}
	if err := validateUploadID(uploadID); err != nil {
		return object.PartInfo{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.PartInfo{}, err
	}
	up, err := e.store.GetUpload(ctx, bucket, key, uploadID)
	if err != nil {
		return object.PartInfo{}, err
	}
	srcRec, err := e.readSource(ctx, src, opts.SourceConditions)
	if err != nil {
		return object.PartInfo{}, err
	}
	c := contentOf(srcRec)
	off, length, err := c.window(opts.SourceRange)
	if err != nil {
		return object.PartInfo{}, err
	}
	if err := validateSize(length, object.MaxPartSize, s3err.EntityTooLarge); err != nil {
		return object.PartInfo{}, err
	}
	dg, err := newDigests(up.Options.ChecksumAlgorithm)
	if err != nil {
		return object.PartInfo{}, wrapCause("checksum", err)
	}
	set, err := e.sets.Place(ctx, bucket, key, length)
	if err != nil {
		return object.PartInfo{}, wrapCause("placing data", err)
	}
	part, err := e.copyPart(ctx, set, c, off, length, dg, partNumber, e.now())
	if err != nil {
		return object.PartInfo{}, err
	}
	return e.storePart(ctx, bucket, key, uploadID, part)
}

// storePart records a written part in the upload. If the upload vanished
// meanwhile, the data is queued for deletion. The part it replaces is queued
// too.
func (e *Engine) storePart(ctx context.Context, bucket, key, uploadID string, part meta.PartRecord) (object.PartInfo, error) {
	garbage, err := e.store.PutPart(ctx, bucket, key, uploadID, part)
	if err != nil {
		e.reclaimParts([]meta.PartRecord{part})
		return object.PartInfo{}, err
	}
	if garbage != nil {
		e.reclaimParts([]meta.PartRecord{*garbage})
	}
	return partInfo(part), nil
}
