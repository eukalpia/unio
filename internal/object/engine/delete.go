package engine

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// DeleteObject implements object.Layer. The store removes the version (or
// adds a delete marker in a versioned bucket) and returns the versions it
// made garbage. Their data is queued for deletion after the commit (I5).
func (e *Engine) DeleteObject(ctx context.Context, bucket, key string, opts object.DeleteOptions) (object.DeleteResult, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.DeleteResult{}, err
	}
	if err := checkCtx(ctx); err != nil {
		return object.DeleteResult{}, err
	}
	res, garbage, err := e.store.DeleteObject(ctx, bucket, key, opts.VersionID, opts.Conditions)
	if err != nil {
		return object.DeleteResult{}, err
	}
	e.reclaim(garbage)
	res.Key = key
	res.VersionID = opts.VersionID
	return res, nil
}

// DeleteObjects implements object.Layer. Keys are deleted with at most
// DeleteConcurrency deletions in flight for this call. A key that fails
// validation or deletion is reported in its result and does not stop the
// others. Results keep the order of the request.
func (e *Engine) DeleteObjects(ctx context.Context, bucket string, objects []object.ToDelete) ([]object.DeleteResult, error) {
	if err := object.ValidateBucketName(bucket); err != nil {
		return nil, err
	}
	if len(objects) > object.MaxDeleteObjects {
		return nil, s3err.Errorf(s3err.MalformedXML, "more than %d objects in one request", object.MaxDeleteObjects)
	}
	if _, err := e.store.GetBucket(ctx, bucket); err != nil {
		return nil, err
	}
	results := make([]object.DeleteResult, len(objects))
	var group errgroup.Group
	group.SetLimit(e.deleteConc)
	for i := range objects {
		group.Go(func() error {
			results[i] = e.deleteOne(ctx, bucket, objects[i])
			return nil
		})
	}
	_ = group.Wait() // the group never fails: per-key errors live in results
	if err := checkCtx(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

// deleteOne deletes one key of a DeleteObjects request.
func (e *Engine) deleteOne(ctx context.Context, bucket string, o object.ToDelete) object.DeleteResult {
	res := object.DeleteResult{Key: o.Key, VersionID: o.VersionID}
	if err := object.ValidateKey(o.Key); err != nil {
		res.Err = err
		return res
	}
	out, garbage, err := e.store.DeleteObject(ctx, bucket, o.Key, o.VersionID, object.Conditions{})
	if err != nil {
		res.Err = err
		return res
	}
	e.reclaim(garbage)
	out.Key = o.Key
	out.VersionID = o.VersionID
	return out
}
