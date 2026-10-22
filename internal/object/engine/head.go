package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
)

// HeadObject implements object.Layer. It evaluates the same conditions as
// GetObject and ignores the range. With PartNumber, Info.Size is the size of
// the selected part.
func (e *Engine) HeadObject(ctx context.Context, bucket, key string, opts object.GetOptions) (object.Info, error) {
	if err := validateBucketKey(bucket, key); err != nil {
		return object.Info{}, err
	}
	if err := checkPartNumberOpt(opts.PartNumber); err != nil {
		return object.Info{}, err
	}
	rec, err := e.lookupRead(ctx, bucket, key, opts.VersionID)
	if err != nil {
		return object.Info{}, err
	}
	info := infoOf(rec, opts.VersionID == "")
	if err := evaluateRead(opts.Conditions, info); err != nil {
		return object.Info{}, err
	}
	c, err := selectPart(rec, contentOf(rec), opts.PartNumber)
	if err != nil {
		return object.Info{}, err
	}
	info.Size = c.size
	return info, nil
}
