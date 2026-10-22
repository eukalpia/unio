package engine

import (
	"context"
	"io"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// CopyObject implements object.Layer.
//
// The source is read through its verified reader and streamed into the
// destination's erasure set, so a copy never buffers more than one stripe
// (unless the destination is inline). A source that is inline stays inline.
// The copy's ETag and checksum are computed while the bytes flow.
func (e *Engine) CopyObject(ctx context.Context, src object.Ref, dstBucket, dstKey string, opts object.CopyOptions) (object.Info, error) {
	if err := validateRef(src); err != nil {
		return object.Info{}, err
	}
	if err := validateBucketKey(dstBucket, dstKey); err != nil {
		return object.Info{}, err
	}
	if opts.SourceRange != nil {
		return object.Info{}, s3err.Errorf(s3err.InvalidArgument, "a range is only valid for part copies")
	}
	if err := validatePutOptions(opts.Put); err != nil {
		return object.Info{}, err
	}
	if src.Bucket == dstBucket && src.Key == dstKey && opts.Directive == object.MetadataCopy {
		return object.Info{}, s3err.Errorf(s3err.InvalidRequest,
			"this copy would replace the object with itself without changing its metadata")
	}
	if err := checkCtx(ctx); err != nil {
		return object.Info{}, err
	}
	srcRec, err := e.readSource(ctx, src, opts.SourceConditions)
	if err != nil {
		return object.Info{}, err
	}
	c := contentOf(srcRec)
	if c.size > object.MaxPutSize {
		return object.Info{}, s3err.Errorf(s3err.InvalidRequest,
			"the copy source is larger than the maximum copy size of %d bytes", int64(object.MaxPutSize))
	}
	put := copyPutOptions(srcRec, opts)
	plan, err := e.planVersion(ctx, dstBucket)
	if err != nil {
		return object.Info{}, err
	}
	rec := newVersion(dstBucket, dstKey, plan.id, e.now(), put)
	dg, err := newDigests(put.ChecksumAlgorithm)
	if err != nil {
		return object.Info{}, wrapCause("checksum", err)
	}
	if c.size <= e.inline {
		data, err := e.readInline(ctx, c, dg)
		if err != nil {
			return object.Info{}, err
		}
		rec.Inline = data
	} else {
		set, err := e.sets.Place(ctx, dstBucket, dstKey, c.size)
		if err != nil {
			return object.Info{}, wrapCause("placing data", err)
		}
		part, err := e.copyPart(ctx, set, c, 0, c.size, dg, 0, rec.ModTime)
		if err != nil {
			return object.Info{}, err
		}
		rec.Parts = []meta.PartRecord{part}
	}
	rec.Size = c.size
	rec.ETag = hexOf(dg.md5Sum())
	rec.Checksum = dg.checksum()
	garbage, err := e.store.PutObject(ctx, rec, opts.Put.Conditions)
	if err != nil {
		return object.Info{}, e.commitFailed(ctx, dstBucket, plan, err, rec.Parts)
	}
	e.reclaim(garbage)
	return infoOf(rec, true), nil
}

// readSource fetches the source version of a copy and checks the copy-source
// preconditions. As in S3, a failed precondition is 412 even for
// If-None-Match.
func (e *Engine) readSource(ctx context.Context, src object.Ref, conds object.Conditions) (meta.ObjectRecord, error) {
	rec, err := e.lookupRead(ctx, src.Bucket, src.Key, src.VersionID)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	if err := conds.EvaluateRead(infoOf(rec, src.VersionID == "")); err != nil {
		if s3err.Is(err, s3err.NotModified) {
			return meta.ObjectRecord{}, s3err.Errorf(s3err.PreconditionFailed, "copy source precondition failed")
		}
		return meta.ObjectRecord{}, err
	}
	return rec, nil
}

// copyPutOptions returns the attributes of the destination. With the copy
// directive they come from the source; with the replace directive, from the
// request. The checksum algorithm is the requested one, or else the source's
// full-object algorithm under the copy directive.
func copyPutOptions(src meta.ObjectRecord, opts object.CopyOptions) object.PutOptions {
	put := opts.Put
	if opts.Directive == object.MetadataCopy {
		put = object.PutOptions{
			ContentType:        src.ContentType,
			ContentEncoding:    src.ContentEncoding,
			ContentDisposition: src.ContentDisposition,
			ContentLanguage:    src.ContentLanguage,
			CacheControl:       src.CacheControl,
			Expires:            src.Expires,
			UserMetadata:       cloneMap(src.UserMetadata),
			Tags:               src.Tags,
			StorageClass:       src.StorageClass,
			ChecksumAlgorithm:  opts.Put.ChecksumAlgorithm,
		}
		if put.ChecksumAlgorithm == object.ChecksumNone && src.Checksum.Type == object.ChecksumFullObject {
			put.ChecksumAlgorithm = src.Checksum.Algorithm
		}
	}
	put.Conditions = opts.Put.Conditions
	return put
}

// readInline copies a small source into memory and feeds the digests.
func (e *Engine) readInline(ctx context.Context, c content, dg *digests) ([]byte, error) {
	rc, err := e.open(ctx, c, 0, c.size)
	if err != nil {
		return nil, err
	}
	data := make([]byte, c.size)
	if _, err := io.ReadFull(rc, data); err != nil {
		_ = rc.Close()
		return nil, wrapCause("reading source", err)
	}
	if err := rc.Close(); err != nil {
		return nil, wrapCause("closing source", err)
	}
	_, _ = dg.writer().Write(data)
	return data, nil
}

// copyPart streams [off, off+length) of c into set as part number, feeding
// dg with every byte. The shards are removed by the set on failure, and by
// the caller if the metadata commit fails.
func (e *Engine) copyPart(ctx context.Context, set erasure.Set, c content, off, length int64, dg *digests, number int, now time.Time) (meta.PartRecord, error) {
	release, err := e.acquireWrite(ctx)
	if err != nil {
		return meta.PartRecord{}, err
	}
	defer release()
	rc, err := e.open(ctx, c, off, length)
	if err != nil {
		return meta.PartRecord{}, err
	}
	id := ids.NewDataID()
	layout, err := set.Write(ctx, id, io.TeeReader(rc, dg.writer()), length)
	if err != nil {
		_ = rc.Close()
		return meta.PartRecord{}, wrapCause("writing copy", err)
	}
	if err := rc.Close(); err != nil {
		e.reclaimParts([]meta.PartRecord{{Data: id, Layout: layout}})
		return meta.PartRecord{}, wrapCause("closing source", err)
	}
	return meta.PartRecord{
		Number:   number,
		Size:     length,
		ETag:     hexOf(dg.md5Sum()),
		Checksum: dg.checksum(),
		ModTime:  now,
		Data:     id,
		Layout:   layout,
	}, nil
}
