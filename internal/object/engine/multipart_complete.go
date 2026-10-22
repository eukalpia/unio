package engine

import (
	"bytes"
	"context"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// CompleteMultipartUpload implements object.Layer.
//
// The request is checked against the stored parts: order, existence, ETag
// and, for uploads with a checksum algorithm, the part checksums. Every part
// except the last must reach object.MinPartSize. The metadata store commits
// the object atomically, checks the parts again and the conditions, and
// returns the parts that the object does not use. Those parts, and the
// versions the commit replaces, are queued for deletion after the commit
// (I5). A failed completion leaves the upload and its parts intact, so the
// client can retry.
func (e *Engine) CompleteMultipartUpload(ctx context.Context, bucket, key, uploadID string, parts []object.CompletePart, opts object.PutOptions) (object.Info, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.Info{}, err
	}
	if err := validateUploadID(uploadID); err != nil {
		return object.Info{}, err
	}
	if err := checkCompleteShape(parts); err != nil {
		return object.Info{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.Info{}, err
	}
	up, err := e.store.GetUpload(ctx, bucket, key, uploadID)
	if err != nil {
		return object.Info{}, err
	}
	stored, err := e.listAllParts(ctx, bucket, key, uploadID)
	if err != nil {
		return object.Info{}, err
	}
	chosen, total, err := matchParts(parts, stored, up.Options.ChecksumAlgorithm)
	if err != nil {
		return object.Info{}, err
	}
	versionID, err := e.versionFor(ctx, bucket)
	if err != nil {
		return object.Info{}, err
	}
	put := up.Options
	put.Conditions = opts.Conditions
	rec := newVersion(bucket, key, versionID, e.now(), put)
	rec.Multipart = true
	rec.Parts = chosen
	rec.Size = total
	if rec.ETag, err = multipartETag(chosen); err != nil {
		return object.Info{}, err
	}
	if rec.Checksum, err = compositeChecksum(up.Options.ChecksumAlgorithm, chosen); err != nil {
		return object.Info{}, err
	}
	garbage, unused, err := e.store.CompleteUpload(ctx, uploadID, rec, opts.Conditions)
	if err != nil {
		return object.Info{}, err
	}
	e.reclaim(garbage)
	e.reclaimParts(unused)
	return infoOf(rec, true), nil
}

// checkCompleteShape validates the part list of a completion without looking
// at the upload.
func checkCompleteShape(parts []object.CompletePart) error {
	if len(parts) == 0 {
		return s3err.Errorf(s3err.InvalidRequest, "at least one part is required")
	}
	prev := 0
	for _, p := range parts {
		if err := validatePartNumber(p.PartNumber); err != nil {
			return err
		}
		if p.PartNumber <= prev {
			return s3err.Errorf(s3err.InvalidPartOrder, "parts must be in strictly ascending order")
		}
		prev = p.PartNumber
	}
	return nil
}

// listAllParts returns every stored part of an upload in part-number order,
// reading the store in pages.
func (e *Engine) listAllParts(ctx context.Context, bucket, key, uploadID string) ([]meta.PartRecord, error) {
	var all []meta.PartRecord
	marker := 0
	for {
		page, res, err := e.store.ListParts(ctx, bucket, key, uploadID, object.ListPartsOptions{
			PartNumberMarker: marker,
			MaxParts:         object.MaxListKeys,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if !res.IsTruncated {
			return all, nil
		}
		if res.NextPartNumberMarker <= marker {
			return nil, s3err.Errorf(s3err.InternalError, "part listing does not advance")
		}
		marker = res.NextPartNumberMarker
	}
}

// matchParts pairs the requested parts with the stored ones. It returns the
// stored records in request order and the total size.
func matchParts(parts []object.CompletePart, stored []meta.PartRecord, alg object.ChecksumAlgorithm) ([]meta.PartRecord, int64, error) {
	byNumber := make(map[int]meta.PartRecord, len(stored))
	for _, p := range stored {
		byNumber[p.Number] = p
	}
	chosen := make([]meta.PartRecord, len(parts))
	var total int64
	for i, req := range parts {
		sp, ok := byNumber[req.PartNumber]
		if !ok {
			return nil, 0, s3err.Errorf(s3err.InvalidPart, "part %d was not uploaded", req.PartNumber)
		}
		if normalizeETag(req.ETag) != sp.ETag {
			return nil, 0, s3err.Errorf(s3err.InvalidPart, "the ETag of part %d does not match", req.PartNumber)
		}
		if alg != object.ChecksumNone {
			if sp.Checksum.Algorithm != alg {
				return nil, 0, s3err.Errorf(s3err.InvalidPart, "part %d has no %s checksum", req.PartNumber, alg)
			}
			if req.Checksum.Algorithm != object.ChecksumNone && !bytes.Equal(req.Checksum.Value, sp.Checksum.Value) {
				return nil, 0, s3err.Errorf(s3err.InvalidPart, "the checksum of part %d does not match", req.PartNumber)
			}
		}
		if i < len(parts)-1 && sp.Size < object.MinPartSize {
			return nil, 0, s3err.Errorf(s3err.EntityTooSmall, "part %d is smaller than the minimum part size", req.PartNumber)
		}
		chosen[i] = sp
		total += sp.Size
	}
	if total > object.MaxObjectSize {
		return nil, 0, s3err.Errorf(s3err.EntityTooLarge, "the object would exceed the maximum size")
	}
	return chosen, total, nil
}
