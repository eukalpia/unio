package meta

import (
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
)

// PartRecord is the stored form of one data part: a multipart part, or the
// single part (Number 0) of a non-inline object.
type PartRecord struct {
	Number   int
	Size     int64
	ETag     string
	Checksum object.Checksum
	ModTime  time.Time
	// Data identifies the shards of this part; Layout records where they are.
	Data   ids.DataID
	Layout erasure.Layout
}

// ObjectRecord is the stored form of one object version.
type ObjectRecord struct {
	Bucket    string
	Key       string
	VersionID string // "" for the null version
	ModTime   time.Time
	// DeleteMarker records a delete marker in a versioned bucket. Delete
	// markers carry no data.
	DeleteMarker bool

	Size               int64
	ETag               string
	ContentType        string
	ContentEncoding    string
	ContentDisposition string
	ContentLanguage    string
	CacheControl       string
	Expires            string
	UserMetadata       map[string]string
	Tags               string
	Checksum           object.Checksum
	StorageClass       string

	// Inline holds the content of objects stored in the inline tier. It is
	// non-nil (possibly empty) for inline objects and nil otherwise.
	Inline []byte
	// Parts holds the data parts of non-inline objects, ordered by Number.
	Parts []PartRecord
	// Multipart is true for objects created by CompleteMultipartUpload.
	Multipart bool
}

// Info returns the client-visible description of r. IsLatest is left false;
// callers that know the version is current set it.
func (r ObjectRecord) Info() object.Info {
	info := object.Info{
		Bucket:             r.Bucket,
		Key:                r.Key,
		VersionID:          r.VersionID,
		DeleteMarker:       r.DeleteMarker,
		Size:               r.Size,
		ETag:               r.ETag,
		ModTime:            r.ModTime,
		ContentType:        r.ContentType,
		ContentEncoding:    r.ContentEncoding,
		ContentDisposition: r.ContentDisposition,
		ContentLanguage:    r.ContentLanguage,
		CacheControl:       r.CacheControl,
		Expires:            r.Expires,
		UserMetadata:       r.UserMetadata,
		Tags:               r.Tags,
		Checksum:           r.Checksum,
		StorageClass:       r.StorageClass,
	}
	if r.Multipart {
		info.Parts = len(r.Parts)
	}
	return info
}

// UploadRecord is the stored form of an in-progress multipart upload.
type UploadRecord struct {
	Bucket    string
	Key       string
	UploadID  string
	Initiated time.Time
	// Options holds the attributes the final object will get. Conditions
	// are not stored.
	Options object.PutOptions
}
