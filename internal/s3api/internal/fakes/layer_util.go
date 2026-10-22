package fakes

import (
	"maps"
	"net/url"
	"slices"
	"time"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// now returns the current UTC time at second precision, the granularity of
// object modification times.
func now() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// cloneInfo returns a copy of info that shares no mutable state with it.
func cloneInfo(info object.Info) object.Info {
	info.UserMetadata = maps.Clone(info.UserMetadata)
	info.Checksum = cloneChecksum(info.Checksum)
	return info
}

// cloneChecksum returns c with its digest copied.
func cloneChecksum(c object.Checksum) object.Checksum {
	c.Value = slices.Clone(c.Value)
	return c
}

// clonePartInfo returns a copy of p that shares no mutable state with it.
func clonePartInfo(p object.PartInfo) object.PartInfo {
	p.Checksum = cloneChecksum(p.Checksum)
	return p
}

// clonePutOptions returns a copy of opts that shares no mutable state with it.
func clonePutOptions(opts object.PutOptions) object.PutOptions {
	opts.UserMetadata = maps.Clone(opts.UserMetadata)
	return opts
}

// infoFromPut builds the Info of a new version from the content attributes in
// opts. The checksum is left zero for the caller to set.
func infoFromPut(bucketName, key string, size int64, etag string, opts object.PutOptions) object.Info {
	storageClass := opts.StorageClass
	if storageClass == "" {
		storageClass = defaultStorageClass
	}
	return object.Info{
		Bucket:             bucketName,
		Key:                key,
		IsLatest:           true,
		Size:               size,
		ETag:               etag,
		ModTime:            now(),
		ContentType:        opts.ContentType,
		ContentEncoding:    opts.ContentEncoding,
		ContentDisposition: opts.ContentDisposition,
		ContentLanguage:    opts.ContentLanguage,
		CacheControl:       opts.CacheControl,
		Expires:            opts.Expires,
		UserMetadata:       maps.Clone(opts.UserMetadata),
		Tags:               opts.Tags,
		StorageClass:       storageClass,
	}
}

// checkPutOptions validates the user metadata and tags of a new version.
func checkPutOptions(opts object.PutOptions) error {
	if err := object.ValidateUserMetadata(opts.UserMetadata); err != nil {
		return err
	}
	return validateTags(opts.Tags)
}

// validateTags checks that the URL-encoded tag set parses and holds at most
// object.MaxTags tags.
func validateTags(tags string) error {
	if tags == "" {
		return nil
	}
	parsed, err := url.ParseQuery(tags)
	if err != nil {
		return s3err.Wrap(s3err.InvalidTag, err)
	}
	if len(parsed) > object.MaxTags {
		return s3err.Errorf(s3err.InvalidTag, "an object can have at most %d tags", object.MaxTags)
	}
	return nil
}

// pageSize returns the effective page size for a requested maximum. Zero, or
// anything above object.MaxListKeys, means object.MaxListKeys. A negative
// request is rejected.
func pageSize(requested int) (int, error) {
	switch {
	case requested < 0:
		return 0, s3err.Errorf(s3err.InvalidArgument, "maximum item count must not be negative")
	case requested == 0 || requested > object.MaxListKeys:
		return object.MaxListKeys, nil
	}
	return requested, nil
}

// windowOf resolves an optional byte range against an object of the given
// size. A nil range selects the whole object.
func windowOf(r *object.HTTPRange, size int64) (int64, int64, error) {
	if r == nil {
		return 0, size, nil
	}
	return r.Resolve(size)
}

// unquoteETag removes one pair of surrounding double quotes from an ETag.
func unquoteETag(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
