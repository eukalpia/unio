package handlers

import (
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3api/headers"
	"github.com/eukalpia/unio/internal/s3api/xmlenc"
	"github.com/eukalpia/unio/internal/s3err"
)

// nullVersionID is the version identifier S3 reports for the one version of
// an object in a bucket that has never been versioned.
const nullVersionID = "null"

// ListObjectVersions lists the versions of the objects of a bucket. A bucket
// that has never been versioned holds one version of each object, the null
// version, so the listing is the current listing with version fields. A
// bucket with versioning enabled or suspended is outside the v0.1 scope and
// is answered with NotImplemented.
func (e *Env) ListObjectVersions(c *Call) error {
	q := c.Route.Query
	maxKeys, err := listPageSize(q)
	if err != nil {
		return err
	}
	encoding, err := encodingType(q)
	if err != nil {
		return err
	}
	keyMarker, versionMarker := q.Get("key-marker"), q.Get("version-id-marker")
	if versionMarker != "" && keyMarker == "" {
		return s3err.Errorf(s3err.InvalidArgument, "a version-id marker cannot be specified without a key marker")
	}
	if versionMarker != "" && versionMarker != nullVersionID {
		return s3err.Errorf(s3err.InvalidArgument, "invalid version id specified")
	}
	info, err := e.Layer.GetBucketInfo(c.Ctx, c.Route.Bucket)
	if err != nil {
		return err
	}
	if info.Versioning != object.VersioningUnversioned {
		return s3err.Errorf(s3err.NotImplemented, "listing the versions of a versioned bucket is not supported")
	}

	prefix, delimiter := q.Get("prefix"), q.Get("delimiter")
	page, err := e.listPage(c, prefix, delimiter, keyMarker, maxKeys)
	if err != nil {
		return err
	}
	res := xmlenc.ListVersionsResult{
		Name:            c.Route.Bucket,
		Prefix:          prefix,
		KeyMarker:       keyMarker,
		VersionIDMarker: versionMarker,
		Delimiter:       delimiter,
		EncodingType:    encoding,
		MaxKeys:         maxKeys,
		IsTruncated:     page.IsTruncated,
		Versions:        nullVersions(page.Objects, c.ownerName()),
		CommonPrefixes:  page.CommonPrefixes,
	}
	if page.IsTruncated {
		res.NextKeyMarker = page.NextAfter
		res.NextVersionIDMarker = nullVersionID
	}
	writeXML(c, 200, xmlenc.AppendListObjectVersions(make([]byte, 0, 4096), &res))
	return nil
}

// nullVersions converts the current objects of an unversioned bucket into
// version entries. Each one is the latest and the null version.
func nullVersions(objs []object.Info, owner string) []xmlenc.Version {
	out := make([]xmlenc.Version, 0, len(objs))
	for _, o := range objs {
		out = append(out, xmlenc.Version{
			Key:          o.Key,
			VersionID:    nullVersionID,
			IsLatest:     true,
			LastModified: o.ModTime,
			ETag:         headers.QuoteETag(o.ETag),
			Size:         o.Size,
			StorageClass: defaultStorageClass,
			Owner:        &xmlenc.Owner{ID: owner, DisplayName: owner},
		})
	}
	return out
}
