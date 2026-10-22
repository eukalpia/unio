package engine

import (
	"context"

	"github.com/eukalpia/unio/internal/object"
)

// nullVersion is the version identifier S3 gives the one version of an object
// in a bucket that has never been versioned.
const nullVersion = "null"

// storeVersion maps the version identifier of a request to the identifier the
// metadata store takes. In a bucket that has never been versioned the null
// version is the current version, which the store addresses with "". Any other
// identifier is passed on unchanged.
func storeVersion(info object.BucketInfo, versionID string) string {
	if versionID == nullVersion && info.Versioning == object.VersioningUnversioned {
		return ""
	}
	return versionID
}

// nullVersionOf reads the bucket only when versionID is the null version, and
// returns the identifier the store takes for it.
func (e *Engine) nullVersionOf(ctx context.Context, bucket, versionID string) (string, error) {
	if versionID != nullVersion {
		return versionID, nil
	}
	info, err := e.store.GetBucket(ctx, bucket)
	if err != nil {
		return "", err
	}
	return storeVersion(info, versionID), nil
}
