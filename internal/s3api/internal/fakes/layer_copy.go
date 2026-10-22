package fakes

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// CopyObject copies the current version of src to dstBucket/dstKey. Under
// object.MetadataCopy the destination takes the source's attributes; under
// object.MetadataReplace it takes the attributes in opts.Put. The content and
// ETag are shared with the source, which is immutable, so the copy is
// independent of later writes to the source.
func (l *Layer) CopyObject(ctx context.Context, src object.Ref, dstBucket, dstKey string, opts object.CopyOptions) (object.Info, error) {
	if err := ctx.Err(); err != nil {
		return object.Info{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	obj, err := l.sourceLocked(src, opts.SourceConditions)
	if err != nil {
		return object.Info{}, err
	}
	db, err := l.bucketLocked(dstBucket)
	if err != nil {
		return object.Info{}, err
	}
	if err := object.ValidateKey(dstKey); err != nil {
		return object.Info{}, err
	}
	if opts.Directive == object.MetadataReplace {
		if err := checkPutOptions(opts.Put); err != nil {
			return object.Info{}, err
		}
	}
	cur, exists := db.objects[dstKey]
	etag := ""
	if exists {
		etag = cur.info.ETag
	}
	if err := opts.Put.Conditions.EvaluateWrite(exists, etag); err != nil {
		return object.Info{}, err
	}
	info, err := copiedInfo(obj, dstBucket, dstKey, opts)
	if err != nil {
		return object.Info{}, err
	}
	db.objects[dstKey] = &storedObject{info: info, data: obj.data}
	return cloneInfo(info), nil
}

// sourceLocked resolves a copy source and evaluates its conditions. A
// NotModified outcome is reported as PreconditionFailed, as S3 does for
// copy-source conditions. l.mu must be held.
func (l *Layer) sourceLocked(src object.Ref, cond object.Conditions) (*storedObject, error) {
	b, err := l.bucketLocked(src.Bucket)
	if err != nil {
		return nil, err
	}
	if err := object.ValidateKey(src.Key); err != nil {
		return nil, err
	}
	obj, ok := b.objects[src.Key]
	if !ok {
		return nil, s3err.Errorf(s3err.NoSuchKey, "source key %q does not exist", src.Key)
	}
	if src.VersionID != "" {
		return nil, s3err.Errorf(s3err.NoSuchVersion, "source version %q does not exist", src.VersionID)
	}
	if err := cond.EvaluateRead(obj.info); err != nil {
		if s3err.Is(err, s3err.NotModified) {
			return nil, s3err.Errorf(s3err.PreconditionFailed, "copy source precondition failed")
		}
		return nil, err
	}
	return obj, nil
}

// copiedInfo returns the Info of a copy of src stored under dstBucket/dstKey.
func copiedInfo(src *storedObject, dstBucket, dstKey string, opts object.CopyOptions) (object.Info, error) {
	var info object.Info
	if opts.Directive == object.MetadataReplace {
		sum, err := checksumOf(opts.Put.ChecksumAlgorithm, src.data)
		if err != nil {
			return object.Info{}, err
		}
		info = infoFromPut(dstBucket, dstKey, src.info.Size, src.info.ETag, opts.Put)
		info.Checksum = sum
	} else {
		info = cloneInfo(src.info)
		info.Bucket = dstBucket
		info.Key = dstKey
	}
	info.Parts = src.info.Parts
	info.ModTime = now()
	info.IsLatest = true
	info.VersionID = ""
	info.DeleteMarker = false
	return info, nil
}
