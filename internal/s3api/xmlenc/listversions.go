package xmlenc

import "time"

// Version describes one object version in a ListObjectVersions response.
// Owner is emitted only when non-nil.
type Version struct {
	// Key is the object key.
	Key string
	// VersionID is the version identifier, "null" for the null version.
	VersionID string
	// IsLatest reports whether the version is the current one of its key.
	IsLatest bool
	// LastModified is the modification time of the version.
	LastModified time.Time
	// ETag is the entity tag, already quoted.
	ETag string
	// Size is the size of the version in bytes.
	Size int64
	// StorageClass is the storage class name.
	StorageClass string
	// Owner is the version owner, or nil to omit the element.
	Owner *Owner
}

// ListVersionsResult is a ListObjectVersions response.
//
// Delimiter, NextKeyMarker, NextVersionIdMarker and EncodingType are emitted
// only when non-empty. Key-like values are percent-encoded when EncodingType
// is "url"; version identifiers are always escaped as text.
type ListVersionsResult struct {
	// Name is the bucket name.
	Name string
	// Prefix is the listing prefix.
	Prefix string
	// KeyMarker is the key marker supplied by the request.
	KeyMarker string
	// VersionIDMarker is the version marker supplied by the request.
	VersionIDMarker string
	// NextKeyMarker is the key marker for the next page, if any.
	NextKeyMarker string
	// NextVersionIDMarker is the version marker for the next page, if any.
	NextVersionIDMarker string
	// Delimiter is the grouping delimiter, if any.
	Delimiter string
	// EncodingType is "url" when key-like values are percent-encoded.
	EncodingType string
	// MaxKeys is the maximum number of versions requested.
	MaxKeys int
	// IsTruncated reports whether more results are available.
	IsTruncated bool
	// Versions lists the matching versions.
	Versions []Version
	// CommonPrefixes lists the rolled-up common prefixes.
	CommonPrefixes []string
}

// AppendListObjectVersions appends a ListObjectVersions response to dst and
// returns the extended slice. A nil r encodes as the zero value.
func AppendListObjectVersions(dst []byte, r *ListVersionsResult) []byte {
	if r == nil {
		r = &ListVersionsResult{}
	}
	urlEnc := r.EncodingType == encodingURL
	dst = appendRoot(dst, "ListVersionsResult")
	dst = appendTextElem(dst, "Name", r.Name)
	dst = appendKeyElem(dst, "Prefix", r.Prefix, urlEnc)
	dst = appendKeyElem(dst, "KeyMarker", r.KeyMarker, urlEnc)
	dst = appendTextElem(dst, "VersionIdMarker", r.VersionIDMarker)
	if r.NextKeyMarker != "" {
		dst = appendKeyElem(dst, "NextKeyMarker", r.NextKeyMarker, urlEnc)
	}
	if r.NextVersionIDMarker != "" {
		dst = appendTextElem(dst, "NextVersionIdMarker", r.NextVersionIDMarker)
	}
	if r.Delimiter != "" {
		dst = appendKeyElem(dst, "Delimiter", r.Delimiter, urlEnc)
	}
	if r.EncodingType != "" {
		dst = appendTextElem(dst, "EncodingType", r.EncodingType)
	}
	dst = appendIntElem(dst, "MaxKeys", int64(r.MaxKeys))
	dst = appendBoolElem(dst, "IsTruncated", r.IsTruncated)
	for i := range r.Versions {
		dst = appendVersion(dst, &r.Versions[i], urlEnc)
	}
	for _, p := range r.CommonPrefixes {
		dst = appendCommonPrefix(dst, p, urlEnc)
	}
	return appendClose(dst, "ListVersionsResult")
}

// appendVersion appends one Version entry.
func appendVersion(dst []byte, v *Version, urlEnc bool) []byte {
	dst = appendOpen(dst, "Version")
	dst = appendKeyElem(dst, "Key", v.Key, urlEnc)
	dst = appendTextElem(dst, "VersionId", v.VersionID)
	dst = appendBoolElem(dst, "IsLatest", v.IsLatest)
	dst = appendTimeElem(dst, "LastModified", v.LastModified)
	dst = appendTextElem(dst, "ETag", v.ETag)
	dst = appendIntElem(dst, "Size", v.Size)
	dst = appendTextElem(dst, "StorageClass", v.StorageClass)
	if v.Owner != nil {
		dst = appendOwnerElem(dst, "Owner", v.Owner)
	}
	return appendClose(dst, "Version")
}
