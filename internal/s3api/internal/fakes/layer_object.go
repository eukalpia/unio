package fakes

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// PutObject stores body as the current version of bucketName/key. The body
// is read to completion before the layer lock is taken, and nothing is
// committed when the read fails. opts.Conditions are evaluated against the
// current version at commit time.
func (l *Layer) PutObject(ctx context.Context, bucketName, key string, body object.Body, opts object.PutOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	if err := l.checkBucketKey(bucketName, key); err != nil {
		return object.Info{}, err
	}
	if body == nil {
		return object.Info{}, s3err.Errorf(s3err.InvalidRequest, "object body is required")
	}
	if err := checkPutOptions(opts); err != nil {
		return object.Info{}, err
	}
	if body.Size() > object.MaxPutSize {
		return object.Info{}, s3err.Errorf(s3err.EntityTooLarge, "object exceeds %d bytes", int64(object.MaxPutSize))
	}
	data, err := readPayload(body)
	if err != nil {
		return object.Info{}, err
	}
	digest := body.MD5()
	info := infoFromPut(bucketName, key, int64(len(data)), hex.EncodeToString(digest[:]), opts)
	info.Checksum = cloneChecksum(body.Checksum())

	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.Info{}, err
	}
	cur, exists := b.objects[key]
	etag := ""
	if exists {
		etag = cur.info.ETag
	}
	if err := opts.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.Info{}, err
	}
	b.objects[key] = &storedObject{info: info, data: data}
	return cloneInfo(info), nil
}

// GetObject returns a reader over the requested window of the current
// version of bucketName/key, after evaluating opts.Conditions and opts.Range.
func (l *Layer) GetObject(ctx context.Context, bucketName, key string, opts object.GetOptions) (*object.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obj, err := l.readObject(bucketName, key, opts)
	if err != nil {
		return nil, err
	}
	off, n, err := windowOf(opts.Range, obj.info.Size)
	if err != nil {
		return nil, err
	}
	return &object.Reader{
		ReadCloser: io.NopCloser(bytes.NewReader(obj.data[off : off+n])),
		Info:       cloneInfo(obj.info),
		Offset:     off,
		Length:     n,
	}, nil
}

// HeadObject returns the description of the current version of
// bucketName/key, after evaluating opts.Conditions.
func (l *Layer) HeadObject(ctx context.Context, bucketName, key string, opts object.GetOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	obj, err := l.readObject(bucketName, key, opts)
	if err != nil {
		return object.Info{}, err
	}
	return cloneInfo(obj.info), nil
}

// readObject looks up the current version of bucketName/key and checks the
// version and read preconditions in opts. The returned object is immutable,
// so the caller may use it after the lock is released.
func (l *Layer) readObject(bucketName, key string, opts object.GetOptions) (*storedObject, error) {
	if opts.PartNumber != 0 {
		return nil, s3err.Errorf(s3err.NotImplemented, "partNumber is not supported by the fake layer")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return nil, err
	}
	if err := object.ValidateKey(key); err != nil {
		return nil, err
	}
	obj, ok := b.objects[key]
	if !ok {
		return nil, s3err.Errorf(s3err.NoSuchKey, "key %q does not exist", key)
	}
	if opts.VersionID != "" {
		return nil, s3err.Errorf(s3err.NoSuchVersion, "version %q does not exist", opts.VersionID)
	}
	if err := opts.Conditions.EvaluateRead(obj.info); err != nil {
		return nil, err
	}
	return obj, nil
}

// DeleteObject removes the current version of bucketName/key. Deleting a
// missing key succeeds. opts.Conditions, when set, are evaluated with write
// semantics against the current version.
func (l *Layer) DeleteObject(ctx context.Context, bucketName, key string, opts object.DeleteOptions) (object.DeleteResult, error) {
	if err := ctx.Err(); err != nil {
		return object.DeleteResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.DeleteResult{}, err
	}
	if err := object.ValidateKey(key); err != nil {
		return object.DeleteResult{}, err
	}
	if opts.VersionID != "" {
		return object.DeleteResult{}, s3err.Errorf(s3err.NoSuchVersion, "version %q does not exist", opts.VersionID)
	}
	if err := b.deleteLocked(key, opts.Conditions); err != nil {
		return object.DeleteResult{}, err
	}
	return object.DeleteResult{Key: key}, nil
}

// DeleteObjects deletes up to object.MaxDeleteObjects keys and reports the
// outcome of each in input order. Invalid keys and version requests fail
// only their own entry.
func (l *Layer) DeleteObjects(ctx context.Context, bucketName string, objects []object.ToDelete) ([]object.DeleteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return nil, err
	}
	if len(objects) > object.MaxDeleteObjects {
		return nil, s3err.Errorf(s3err.InvalidArgument, "at most %d keys can be deleted in one request", object.MaxDeleteObjects)
	}
	results := make([]object.DeleteResult, 0, len(objects))
	for _, o := range objects {
		results = append(results, b.deleteOne(o))
	}
	return results, nil
}

// deleteLocked removes key from b when cond holds for the current version.
// l.mu must be held.
func (b *bucket) deleteLocked(key string, cond object.Conditions) error {
	if !cond.IsZero() {
		cur, exists := b.objects[key]
		etag := ""
		if exists {
			etag = cur.info.ETag
		}
		if err := cond.EvaluateWrite(exists, etag); err != nil {
			return err
		}
	}
	delete(b.objects, key)
	return nil
}

// deleteOne deletes one entry of a DeleteObjects request. l.mu must be held.
func (b *bucket) deleteOne(o object.ToDelete) object.DeleteResult {
	res := object.DeleteResult{Key: o.Key}
	if err := object.ValidateKey(o.Key); err != nil {
		res.Err = err
		return res
	}
	if o.VersionID != "" {
		res.Err = s3err.Errorf(s3err.NoSuchVersion, "version %q does not exist", o.VersionID)
		return res
	}
	delete(b.objects, o.Key)
	return res
}
