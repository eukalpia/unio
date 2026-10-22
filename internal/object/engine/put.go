package engine

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// PutObject implements object.Layer.
//
// Payloads up to the inline threshold are read into a pooled buffer and kept
// in the metadata record. Larger payloads are written to one erasure set
// first, and the metadata is committed only after the body ended cleanly and
// the write reached its quorum (I1, I4). The replaced version is queued for
// deletion after the commit (I5).
func (e *Engine) PutObject(ctx context.Context, bucket, key string, body object.Body, opts object.PutOptions) (object.Info, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.Info{}, err
	}
	if err := validatePutOptions(opts); err != nil {
		return object.Info{}, err
	}
	if err := checkBody(body); err != nil {
		return object.Info{}, err
	}
	size := body.Size()
	if err := validateSize(size, object.MaxPutSize, s3err.EntityTooLarge); err != nil {
		return object.Info{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.Info{}, err
	}
	plan, err := e.planVersion(ctx, bucket)
	if err != nil {
		return object.Info{}, err
	}
	rec := newVersion(bucket, key, plan.id, e.now(), opts)
	if e.inlineFits(size) {
		return e.putInline(ctx, body, rec, plan, opts.Conditions)
	}
	return e.putErasure(ctx, body, rec, plan, opts.Conditions)
}

// newVersion returns a record with the client-visible attributes of opts.
func newVersion(bucket, key, versionID string, now time.Time, opts object.PutOptions) meta.ObjectRecord {
	return meta.ObjectRecord{
		Bucket:             bucket,
		Key:                key,
		VersionID:          versionID,
		ModTime:            now,
		ContentType:        opts.ContentType,
		ContentEncoding:    opts.ContentEncoding,
		ContentDisposition: opts.ContentDisposition,
		ContentLanguage:    opts.ContentLanguage,
		CacheControl:       opts.CacheControl,
		Expires:            opts.Expires,
		UserMetadata:       cloneMap(opts.UserMetadata),
		Tags:               opts.Tags,
		StorageClass:       opts.StorageClass,
	}
}

// putInline stores a small payload in the metadata record. The buffer comes
// from the engine's pool, so concurrent inline writes stay within the buffer
// budget, and the record gets its own copy.
func (e *Engine) putInline(ctx context.Context, body object.Body, rec meta.ObjectRecord, plan versionPlan, cond object.Conditions) (object.Info, error) {
	size := body.Size()
	buf, err := e.inlinePool.Get(ctx)
	if err != nil {
		return object.Info{}, wrapCause("buffer", err)
	}
	defer buf.Release()
	data := buf.Bytes()[:size]
	guard := newBodyGuard(body)
	if _, err := io.ReadFull(guard, data); err != nil {
		return object.Info{}, bodyError(err)
	}
	if err := guard.finish(); err != nil {
		return object.Info{}, err
	}
	rec.Size = size
	rec.Inline = make([]byte, size)
	copy(rec.Inline, data)
	rec.ETag = hexOf(body.MD5())
	rec.Checksum = cloneChecksum(body.Checksum())
	garbage, err := e.store.PutObject(ctx, rec, cond)
	if err != nil {
		return object.Info{}, e.commitFailed(ctx, rec.Bucket, plan, err, nil)
	}
	e.reclaim(garbage)
	return infoOf(rec, true), nil
}

// putErasure stores a payload as the single part of an erasure-coded object.
func (e *Engine) putErasure(ctx context.Context, body object.Body, rec meta.ObjectRecord, plan versionPlan, cond object.Conditions) (object.Info, error) {
	set, err := e.sets.Place(ctx, rec.Bucket, rec.Key, body.Size())
	if err != nil {
		return object.Info{}, wrapCause("placing data", err)
	}
	part, err := e.writePart(ctx, set, body, 0, rec.ModTime)
	if err != nil {
		return object.Info{}, err
	}
	rec.Size = part.Size
	rec.ETag = part.ETag
	rec.Checksum = part.Checksum
	rec.Parts = []meta.PartRecord{part}
	garbage, err := e.store.PutObject(ctx, rec, cond)
	if err != nil {
		return object.Info{}, e.commitFailed(ctx, rec.Bucket, plan, err, rec.Parts)
	}
	e.reclaim(garbage)
	return infoOf(rec, true), nil
}

// writePart writes body as the erasure-coded data of one part, numbered
// number, to set. It returns the record of the durable shards.
//
// If the body does not end cleanly, or the write fails, nothing becomes
// visible: the set removes the shards it wrote on a failed write, and shards
// of a body that failed after a successful write are queued for deletion.
func (e *Engine) writePart(ctx context.Context, set erasure.Set, body object.Body, number int, now time.Time) (meta.PartRecord, error) {
	release, err := e.acquireWrite(ctx)
	if err != nil {
		return meta.PartRecord{}, err
	}
	defer release()
	size := body.Size()
	id := ids.NewDataID()
	guard := newBodyGuard(body)
	layout, err := set.Write(ctx, id, guard, size)
	if err != nil {
		// The body's own failure is reported as such. A body that ended early
		// is the client's fault, whatever error the set wrapped around it.
		switch {
		case guard.err != nil:
			return meta.PartRecord{}, bodyError(guard.err)
		case guard.eof && guard.n < size:
			return meta.PartRecord{}, s3err.Errorf(s3err.IncompleteBody,
				"request body ended after %d of %d bytes", guard.n, size)
		}
		return meta.PartRecord{}, wrapCause("writing data", err)
	}
	if err := guard.finish(); err != nil {
		e.reclaimParts([]meta.PartRecord{{Data: id, Layout: layout}})
		return meta.PartRecord{}, err
	}
	return meta.PartRecord{
		Number:   number,
		Size:     size,
		ETag:     hexOf(body.MD5()),
		Checksum: cloneChecksum(body.Checksum()),
		ModTime:  now,
		Data:     id,
		Layout:   layout,
	}, nil
}

// bodyError maps a failure while reading the request body to the error the
// client sees. A payload that ends early is IncompleteBody. Any other error
// keeps the S3 code it carries.
func bodyError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return s3err.Wrap(s3err.IncompleteBody, err)
	}
	return wrapCause("reading body", err)
}
