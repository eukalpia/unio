package metatest

import (
	"crypto/md5" // #nosec G501 -- MD5 is the S3 ETag algorithm; test data only, never used for security
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/versionid"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// The helpers in this file build test data and check results. They hold no
// state, so every suite case builds its own data.

// testBucket is the bucket most cases use.
const testBucket = "suite-bucket"

// testOwner is the owner of buckets created by mustCreateBucket.
const testOwner = "owner-a"

// testTime returns the ModTime used by generated records.
func testTime() time.Time {
	return time.Date(2026, time.January, 2, 3, 4, 5, 6, time.UTC)
}

// newVersionID returns a version id in the production format.
func newVersionID() string { return versionid.New() }

// etagOf returns the ETag of body: its hex MD5, without quotes.
func etagOf(body string) string {
	// #nosec G401 -- MD5 is the S3 ETag algorithm, not a security primitive; test data only.
	sum := md5.Sum([]byte(body))
	return hex.EncodeToString(sum[:])
}

// crc32Checksum returns a CRC32C checksum of body.
func crc32Checksum(body string) object.Checksum {
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], crc32.Checksum([]byte(body), crc32.MakeTable(crc32.Castagnoli)))
	return object.Checksum{Algorithm: object.ChecksumCRC32C, Value: v[:]}
}

// objectRecord returns a single-object inline record for body. The record
// has user metadata, a checksum and a content type, so that round trips
// exercise every common field.
func objectRecord(bucket, key, versionID, body string) meta.ObjectRecord {
	return meta.ObjectRecord{
		Bucket:       bucket,
		Key:          key,
		VersionID:    versionID,
		ModTime:      testTime(),
		Size:         int64(len(body)),
		ETag:         etagOf(body),
		ContentType:  "text/plain",
		UserMetadata: map[string]string{"suite": key},
		Checksum:     crc32Checksum(body),
		StorageClass: "STANDARD",
		Inline:       []byte(body),
	}
}

// testLayout returns a three-shard erasure layout with fresh drive ids.
func testLayout() erasure.Layout {
	return erasure.Layout{
		Set:          1,
		DataShards:   2,
		ParityShards: 1,
		StripeSize:   erasure.DefaultStripeSize,
		Drives:       []ids.DriveID{ids.NewDriveID(), ids.NewDriveID(), ids.NewDriveID()},
	}
}

// partRecord returns a stored part for body with a fresh data id.
func partRecord(number int, body string) meta.PartRecord {
	return meta.PartRecord{
		Number:   number,
		Size:     int64(len(body)),
		ETag:     etagOf(body),
		Checksum: crc32Checksum(body),
		ModTime:  testTime(),
		Data:     ids.NewDataID(),
		Layout:   testLayout(),
	}
}

// uploadRecord returns an upload of key with id uploadID in bucket.
func uploadRecord(bucket, key, uploadID string) meta.UploadRecord {
	return meta.UploadRecord{
		Bucket:    bucket,
		Key:       key,
		UploadID:  uploadID,
		Initiated: testTime(),
		Options: object.PutOptions{
			ContentType:       "application/octet-stream",
			UserMetadata:      map[string]string{"upload": uploadID},
			ChecksumAlgorithm: object.ChecksumCRC32C,
		},
	}
}

// mustCreateBucket creates bucket with testOwner and the given versioning.
func mustCreateBucket(t *testing.T, s meta.Store, name string, versioning object.VersioningState) object.BucketInfo {
	t.Helper()
	info, err := s.CreateBucket(t.Context(), name, object.MakeBucketOptions{Owner: testOwner, Versioning: versioning})
	mustNoErr(t, err)
	return info
}

// mustPutObject writes rec with no conditions and returns any garbage.
func mustPutObject(t *testing.T, s meta.Store, rec meta.ObjectRecord) []meta.ObjectRecord {
	t.Helper()
	garbage, err := s.PutObject(t.Context(), rec, object.Conditions{})
	mustNoErr(t, err)
	return garbage
}

// mustPut writes value to a SystemKV key and returns the new revision.
func mustPut(t *testing.T, kv meta.SystemKV, key, value string, expect uint64) uint64 {
	t.Helper()
	rev, err := kv.Put(t.Context(), key, []byte(value), expect)
	mustNoErr(t, err)
	return rev
}

// mustNoErr fails the test when err is not nil.
func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// requireCode fails the test unless err carries the S3 code want.
func requireCode(t *testing.T, err error, want s3err.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want code %s", want)
	}
	if got := s3err.CodeOf(err); got != want {
		t.Fatalf("error code = %s (%v), want %s", got, err, want)
	}
}

// requireSentinel fails the test unless errors.Is(err, want).
func requireSentinel(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// requireCount fails the test unless got has length want.
func requireCount[T any](t *testing.T, what string, got []T, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("%s: got %d entries, want %d: %+v", what, len(got), want, got)
	}
}

// requireEqualStrings fails the test unless got equals want.
func requireEqualStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("%s mismatch (-want +got):\n%s", what, diff)
	}
}

// requireSameRecord fails the test unless two object records are equal.
// Nil and empty collections compare equal, except that Inline must keep its
// nil-ness, because the contract gives it meaning.
func requireSameRecord(t *testing.T, want, got meta.ObjectRecord) {
	t.Helper()
	if (want.Inline == nil) != (got.Inline == nil) {
		t.Fatalf("Inline nil-ness differs: want nil=%v, got nil=%v", want.Inline == nil, got.Inline == nil)
	}
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("record mismatch (-want +got):\n%s", diff)
	}
}

// requireSameUpload fails the test unless two upload records are equal.
func requireSameUpload(t *testing.T, want, got meta.UploadRecord) {
	t.Helper()
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("upload mismatch (-want +got):\n%s", diff)
	}
}

// partNumbers returns the numbers of parts in order.
func partNumbers(parts []meta.PartRecord) []int {
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.Number)
	}
	return out
}

// requireNumbers fails the test unless parts carry exactly the numbers want.
func requireNumbers(t *testing.T, what string, parts []meta.PartRecord, want ...int) {
	t.Helper()
	if diff := cmp.Diff(want, partNumbers(parts), cmpopts.EquateEmpty()); diff != "" {
		t.Fatalf("%s part numbers mismatch (-want +got):\n%s", what, diff)
	}
}
