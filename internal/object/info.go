package object

import "time"

// Info is the client-visible description of an object version.
type Info struct {
	Bucket    string
	Key       string
	VersionID string // "" for the null version
	IsLatest  bool
	// DeleteMarker is true when this version is a delete marker.
	DeleteMarker bool
	Size         int64
	// ETag is the hex MD5 of the content, or "<hex>-<parts>" for multipart
	// objects. It is stored and returned without quotes.
	ETag    string
	ModTime time.Time

	ContentType        string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	CacheControl       string
	Expires            string
	// UserMetadata holds x-amz-meta-* values keyed by the lower-case name
	// without the prefix.
	UserMetadata map[string]string
	// Tags is the URL-encoded tag set ("k1=v1&k2=v2").
	Tags         string
	Checksum     Checksum
	StorageClass string
	// Parts is the number of multipart parts, 0 for single-part objects.
	Parts int
}

// Ref names an object version, used as a copy source.
type Ref struct {
	Bucket    string
	Key       string
	VersionID string
}
