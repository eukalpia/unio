package meta

import (
	"context"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
)

// Store is the metadata plane for buckets, object versions and multipart
// state.
//
// Consistency: every method is linearizable (invariant I3). A successful
// write is visible to every subsequent read on any node.
//
// Garbage: methods that make data unreachable return the affected records.
// The caller deletes their data after the call returns successfully, never
// before (invariant I5). A crash in between leaves orphans for GC.
//
// Buckets are addressed by name; the store resolves names to BucketIDs.
// Operations on missing buckets fail with s3err.NoSuchBucket.
type Store interface {
	// CreateBucket fails with BucketAlreadyOwnedByYou if the bucket exists
	// with the same owner, BucketAlreadyExists otherwise.
	CreateBucket(ctx context.Context, name string, opts object.MakeBucketOptions) (object.BucketInfo, error)
	GetBucket(ctx context.Context, name string) (object.BucketInfo, error)
	// ListBuckets returns all buckets ordered by name.
	ListBuckets(ctx context.Context) ([]object.BucketInfo, error)
	// DeleteBucket fails with BucketNotEmpty if the bucket holds any object
	// version or in-progress upload. Concurrent writes to a bucket being
	// deleted fail with NoSuchBucket or make the delete fail; they never
	// leave unreachable records.
	DeleteBucket(ctx context.Context, name string) error
	SetBucketVersioning(ctx context.Context, name string, state object.VersioningState) error

	// PutObject commits rec as the newest version of rec.Bucket/rec.Key.
	// In unversioned buckets rec.VersionID must be "" and replaces the null
	// version. cond is evaluated atomically against the current latest
	// version with Conditions.EvaluateWrite. Returns replaced versions.
	PutObject(ctx context.Context, rec ObjectRecord, cond object.Conditions) (garbage []ObjectRecord, err error)

	// GetObject returns a version: the latest when versionID is "". Delete
	// markers are returned as records with DeleteMarker set. Fails with
	// NoSuchKey when the key has no versions and NoSuchVersion when
	// versionID is unknown.
	GetObject(ctx context.Context, bucket, key, versionID string) (ObjectRecord, error)

	// DeleteObject removes a version (versionID != "") or, with versionID ""
	// in a versioned bucket, adds a delete marker; in an unversioned bucket
	// it removes the null version. Deleting a missing key succeeds. cond is
	// evaluated atomically.
	DeleteObject(ctx context.Context, bucket, key, versionID string, cond object.Conditions) (object.DeleteResult, []ObjectRecord, error)

	// ListObjects lists the latest non-delete-marker version of each key in
	// key order, honouring Prefix, Delimiter, After and MaxKeys.
	ListObjects(ctx context.Context, bucket string, opts object.ListOptions) (ListResult, error)

	// UpdateLayout atomically replaces the layout of part partNumber (0 for
	// the single part of a non-multipart object) of a version, provided the
	// part still references data. Used by repair. Fails with
	// ErrRevisionMismatch if the version no longer references data.
	UpdateLayout(ctx context.Context, bucket, key, versionID string, partNumber int, data ids.DataID, layout erasure.Layout) error

	// UpdateTags replaces the stored tag set of one version: the latest when
	// versionID is "". tags is the stored form of the set (object.EncodeTags),
	// and the store checks it with object.DecodeTags. Nothing else changes: no
	// version is created, and the data, ETag and modification time stay as
	// they were. The result is the updated record without its inline content.
	// Fails with NoSuchKey when the key has no versions, or when versionID is
	// "" and the latest version is a delete marker; NoSuchVersion when versionID
	// is unknown; MethodNotAllowed when versionID names a delete marker. The
	// check and the write are one atomic step.
	UpdateTags(ctx context.Context, bucket, key, versionID, tags string) (ObjectRecord, error)

	CreateUpload(ctx context.Context, up UploadRecord) error
	// GetUpload fails with NoSuchUpload.
	GetUpload(ctx context.Context, bucket, key, uploadID string) (UploadRecord, error)
	// PutPart stores part, replacing a part with the same number, which is
	// returned as garbage.
	PutPart(ctx context.Context, bucket, key, uploadID string, part PartRecord) (garbage *PartRecord, err error)
	ListParts(ctx context.Context, bucket, key, uploadID string, opts object.ListPartsOptions) ([]PartRecord, object.ListPartsResult, error)
	// CompleteUpload atomically: verifies the upload exists and that every
	// part in rec.Parts is still the stored part with that number (same
	// DataID, else InvalidPart), evaluates cond, commits rec as the newest
	// version and removes the upload. Returns replaced versions and stored
	// parts not used by rec.
	CompleteUpload(ctx context.Context, uploadID string, rec ObjectRecord, cond object.Conditions) (garbage []ObjectRecord, unused []PartRecord, err error)
	// AbortUpload removes the upload and returns its parts.
	AbortUpload(ctx context.Context, bucket, key, uploadID string) ([]PartRecord, error)
	ListUploads(ctx context.Context, bucket string, opts object.ListUploadsOptions) (object.ListUploadsResult, error)
}
