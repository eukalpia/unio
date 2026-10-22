package core

import (
	"context"
	"time"
)

// PartRecord is the stored form of one data part: a multipart part, or the
// single part of a non-inline object.
type PartRecord struct {
	Number   int
	Size     int64
	ETag     string
	Checksum Checksum
	ModTime  time.Time
	// Data identifies the shards of this part; Layout records where they are.
	Data   DataID
	Layout ErasureLayout
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
	Checksum           Checksum
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
func (r ObjectRecord) Info() ObjectInfo {
	info := ObjectInfo{
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
	// Options holds the attributes the final object will get.
	Options PutObjectOptions
}

// ListRecordsResult is a page of object records.
type ListRecordsResult struct {
	Objects        []ObjectRecord
	CommonPrefixes []string
	IsTruncated    bool
	NextAfter      string
}

// MetaStore is the metadata plane: buckets, object versions and multipart
// state. Implemented by internal/meta on Multi-Raft partitions.
//
// Consistency: every method is linearizable (invariant I3). A successful
// write is visible to every subsequent read on any node.
//
// Garbage: methods that make data unreachable return the affected records.
// The caller deletes their data after the call returns successfully, never
// before (invariant I5). A crash in between leaves orphans for GC.
//
// Buckets are addressed by name; the store resolves names to BucketIDs.
// Operations on missing buckets fail with ErrNoSuchBucket.
type MetaStore interface {
	// CreateBucket fails with ErrBucketAlreadyOwnedByYou if the bucket exists
	// with the same owner, ErrBucketAlreadyExists otherwise.
	CreateBucket(ctx context.Context, name string, opts MakeBucketOptions) (BucketInfo, error)
	GetBucket(ctx context.Context, name string) (BucketInfo, error)
	// ListBuckets returns all buckets ordered by name.
	ListBuckets(ctx context.Context) ([]BucketInfo, error)
	// DeleteBucket fails with ErrBucketNotEmpty if the bucket holds any
	// object version or in-progress upload. Concurrent writes to a bucket
	// being deleted fail with ErrNoSuchBucket or make the delete fail; they
	// never leave unreachable records.
	DeleteBucket(ctx context.Context, name string) error
	SetBucketVersioning(ctx context.Context, name string, state VersioningState) error

	// PutObject commits rec as the newest version of rec.Bucket/rec.Key.
	// In unversioned buckets rec.VersionID must be "" and replaces the null
	// version. cond is evaluated atomically against the current latest
	// version with Conditions.EvaluateWrite. Returns replaced versions.
	PutObject(ctx context.Context, rec ObjectRecord, cond Conditions) (garbage []ObjectRecord, err error)

	// GetObject returns a version: the latest when versionID is "". Delete
	// markers are returned as records with DeleteMarker set. Fails with
	// ErrNoSuchKey when the key has no versions and ErrNoSuchVersion when
	// versionID is unknown.
	GetObject(ctx context.Context, bucket, key, versionID string) (ObjectRecord, error)

	// DeleteObject removes a version (versionID != "") or, with versionID ""
	// in a versioned bucket, adds a delete marker; in an unversioned bucket
	// it removes the null version. Deleting a missing key succeeds. cond is
	// evaluated atomically (If-Match on delete).
	DeleteObject(ctx context.Context, bucket, key, versionID string, cond Conditions) (DeleteResult, []ObjectRecord, error)

	// ListObjects lists the latest non-delete-marker version of each key in
	// key order, honouring Prefix, Delimiter, After and MaxKeys.
	ListObjects(ctx context.Context, bucket string, opts ListOptions) (ListRecordsResult, error)

	// UpdateLayout atomically replaces the layout of part partNumber (0 for
	// the single part of a non-multipart object) of a version, provided the
	// part still references data. Used by repair. Fails with
	// ErrRevisionMismatch if the version no longer references data.
	UpdateLayout(ctx context.Context, bucket, key, versionID string, partNumber int, data DataID, layout ErasureLayout) error

	CreateUpload(ctx context.Context, up UploadRecord) error
	// GetUpload fails with ErrNoSuchUpload.
	GetUpload(ctx context.Context, bucket, key, uploadID string) (UploadRecord, error)
	// PutPart stores part, replacing a part with the same number, which is
	// returned as garbage.
	PutPart(ctx context.Context, bucket, key, uploadID string, part PartRecord) (garbage *PartRecord, err error)
	ListParts(ctx context.Context, bucket, key, uploadID string, opts ListPartsOptions) ([]PartRecord, ListPartsResult, error)
	// CompleteUpload atomically: verifies the upload exists and that every
	// part in rec.Parts is still the stored part with that number (same
	// DataID, else ErrInvalidPart), evaluates cond, commits rec as the newest
	// version and removes the upload. Returns replaced versions and stored
	// parts not used by rec.
	CompleteUpload(ctx context.Context, uploadID string, rec ObjectRecord, cond Conditions) (garbage []ObjectRecord, unused []PartRecord, err error)
	// AbortUpload removes the upload and returns its parts.
	AbortUpload(ctx context.Context, bucket, key, uploadID string) ([]PartRecord, error)
	ListUploads(ctx context.Context, bucket string, opts ListUploadsOptions) (ListUploadsResult, error)
}

// AnyRevision makes SystemKV writes unconditional.
const AnyRevision = ^uint64(0)

// KV is a key, value and revision from SystemKV.
type KV struct {
	Key      string
	Value    []byte
	Revision uint64
}

// SystemKV is a small linearizable key-value store in the system group,
// used for IAM, bucket configuration and cluster state. Values are limited
// to 1 MiB. Implemented by internal/meta.
type SystemKV interface {
	// Get fails with ErrNotFound.
	Get(ctx context.Context, key string) (value []byte, revision uint64, err error)
	// Put writes value if the key's current revision equals expectRevision
	// (0 means the key must not exist; AnyRevision skips the check) and
	// returns the new revision. Fails with ErrRevisionMismatch.
	Put(ctx context.Context, key string, value []byte, expectRevision uint64) (uint64, error)
	// Delete removes key under the same revision rules. Deleting a missing
	// key with AnyRevision succeeds.
	Delete(ctx context.Context, key string, expectRevision uint64) error
	// List returns all entries whose key starts with prefix, in key order.
	List(ctx context.Context, prefix string) ([]KV, error)
}
