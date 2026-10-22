package core

import (
	"encoding/base64"
	"time"
)

// VersioningState is the versioning configuration of a bucket.
type VersioningState uint8

// Bucket versioning states.
const (
	VersioningUnversioned VersioningState = iota
	VersioningEnabled
	VersioningSuspended
)

// BucketInfo describes a bucket.
type BucketInfo struct {
	Name       string
	ID         BucketID
	Created    time.Time
	Owner      string
	Versioning VersioningState
}

// ChecksumAlgorithm is an S3 additional checksum algorithm.
type ChecksumAlgorithm uint8

// Supported checksum algorithms.
const (
	ChecksumNone ChecksumAlgorithm = iota
	ChecksumCRC32
	ChecksumCRC32C
	ChecksumCRC64NVME
	ChecksumSHA1
	ChecksumSHA256
)

var checksumNames = [...]string{"", "CRC32", "CRC32C", "CRC64NVME", "SHA1", "SHA256"}

// String returns the S3 name of the algorithm ("CRC32C", ...), or "" for none.
func (a ChecksumAlgorithm) String() string {
	if int(a) < len(checksumNames) {
		return checksumNames[a]
	}
	return ""
}

// ParseChecksumAlgorithm parses an S3 algorithm name, case-sensitively as
// S3 does. It returns false for unknown names.
func ParseChecksumAlgorithm(s string) (ChecksumAlgorithm, bool) {
	for i, n := range checksumNames {
		if i > 0 && n == s {
			return ChecksumAlgorithm(i), true
		}
	}
	return ChecksumNone, false
}

// ChecksumType distinguishes full-object checksums from composite
// (checksum-of-checksums) checksums of multipart objects.
type ChecksumType uint8

// Checksum types.
const (
	ChecksumFullObject ChecksumType = iota
	ChecksumComposite
)

// Checksum is an S3 additional checksum value.
type Checksum struct {
	Algorithm ChecksumAlgorithm
	Type      ChecksumType
	// Value is the raw digest. For composite checksums of multipart objects
	// the S3 representation appends "-<parts>"; see Parts.
	Value []byte
	// Parts is the number of parts for composite checksums, else 0.
	Parts int
}

// IsZero reports whether no checksum is present.
func (c Checksum) IsZero() bool { return c.Algorithm == ChecksumNone }

// Base64 returns the S3 header representation of the checksum value.
func (c Checksum) Base64() string {
	s := base64.StdEncoding.EncodeToString(c.Value)
	if c.Type == ChecksumComposite && c.Parts > 0 {
		s += "-" + itoa(c.Parts)
	}
	return s
}

// ObjectInfo is the client-visible description of an object version.
type ObjectInfo struct {
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

// HTTPRange is a parsed byte range request. Exactly one form is used:
// Start/End (End < 0 means "to the end") or Suffix > 0 (the last Suffix
// bytes).
type HTTPRange struct {
	Start  int64
	End    int64
	Suffix int64
}

// Resolve converts r to an absolute [offset, offset+length) window for an
// object of the given size. It returns ErrInvalidRange when the range does
// not overlap the object, as S3 does.
func (r HTTPRange) Resolve(size int64) (offset, length int64, err error) {
	switch {
	case r.Suffix > 0:
		if size == 0 {
			return 0, 0, Errorf(ErrInvalidRange, "range not satisfiable")
		}
		if r.Suffix > size {
			return 0, size, nil
		}
		return size - r.Suffix, r.Suffix, nil
	case r.Start < 0 || (r.End >= 0 && r.End < r.Start):
		return 0, 0, Errorf(ErrInvalidRange, "invalid range")
	case r.Start >= size:
		return 0, 0, Errorf(ErrInvalidRange, "range not satisfiable")
	}
	end := r.End
	if end < 0 || end >= size {
		end = size - 1
	}
	return r.Start, end - r.Start + 1, nil
}

// Conditions are the HTTP preconditions of a read or write.
type Conditions struct {
	// IfMatch is an ETag (unquoted) or "*".
	IfMatch string
	// IfNoneMatch is an ETag (unquoted) or "*".
	IfNoneMatch       string
	IfModifiedSince   time.Time
	IfUnmodifiedSince time.Time
}

// IsZero reports whether no condition is set.
func (c Conditions) IsZero() bool {
	return c.IfMatch == "" && c.IfNoneMatch == "" && c.IfModifiedSince.IsZero() && c.IfUnmodifiedSince.IsZero()
}

// EvaluateRead applies S3 read precondition semantics to an existing object.
// It returns nil, or an *Error with ErrPreconditionFailed or ErrNotModified.
func (c Conditions) EvaluateRead(info ObjectInfo) error {
	if c.IfMatch != "" && c.IfMatch != "*" && c.IfMatch != info.ETag {
		return Errorf(ErrPreconditionFailed, "If-Match failed")
	}
	if c.IfMatch == "" && !c.IfUnmodifiedSince.IsZero() && info.ModTime.Truncate(time.Second).After(c.IfUnmodifiedSince) {
		return Errorf(ErrPreconditionFailed, "If-Unmodified-Since failed")
	}
	if c.IfNoneMatch != "" && (c.IfNoneMatch == "*" || c.IfNoneMatch == info.ETag) {
		return Errorf(ErrNotModified, "If-None-Match matched")
	}
	if c.IfNoneMatch == "" && !c.IfModifiedSince.IsZero() && !info.ModTime.Truncate(time.Second).After(c.IfModifiedSince) {
		return Errorf(ErrNotModified, "not modified")
	}
	return nil
}

// EvaluateWrite applies S3 conditional-write semantics (If-Match and
// If-None-Match on PUT and CompleteMultipartUpload). exists reports whether
// a current, non-delete-marker version exists; etag is its ETag. Metadata
// stores must evaluate this atomically with the write.
func (c Conditions) EvaluateWrite(exists bool, etag string) error {
	if c.IfNoneMatch == "*" && exists {
		return Errorf(ErrPreconditionFailed, "object already exists")
	}
	if c.IfMatch != "" {
		if !exists {
			return Errorf(ErrNoSuchKey, "object does not exist")
		}
		if c.IfMatch != "*" && c.IfMatch != etag {
			return Errorf(ErrPreconditionFailed, "If-Match failed")
		}
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
