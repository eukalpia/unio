package engine

import (
	"context"
	"slices"

	"github.com/eukalpia/unio/internal/object"
)

// ListObjects implements object.Layer. It returns the current version of each
// key, in key order, with IsLatest set. Delete markers are not listed.
func (e *Engine) ListObjects(ctx context.Context, bucket string, opts object.ListOptions) (object.ListResult, error) {
	if err := object.ValidateBucketName(bucket); err != nil {
		return object.ListResult{}, err
	}
	limit, err := resolveLimit(opts.MaxKeys)
	if err != nil {
		return object.ListResult{}, err
	}
	page, err := e.store.ListObjects(ctx, bucket, object.ListOptions{
		Prefix:    opts.Prefix,
		Delimiter: opts.Delimiter,
		After:     opts.After,
		MaxKeys:   limit,
	})
	if err != nil {
		return object.ListResult{}, err
	}
	res := object.ListResult{
		Objects:        make([]object.Info, 0, len(page.Objects)),
		CommonPrefixes: slices.Clone(page.CommonPrefixes),
		IsTruncated:    page.IsTruncated,
		NextAfter:      page.NextAfter,
	}
	for _, rec := range page.Objects {
		res.Objects = append(res.Objects, infoOf(rec, true))
	}
	return res, nil
}
