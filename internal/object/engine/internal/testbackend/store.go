// Package testbackend provides in-memory implementations of meta.Store and
// of erasure.Set and erasure.Registry for tests of the object engine. They
// follow the contracts of their interfaces closely enough for the engine to
// exercise every path, but they are not production code: nothing is
// persisted and the erasure set replicates whole shards instead of encoding
// them.
package testbackend

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// Store is an in-memory meta.Store. It is safe for concurrent use.
type Store struct {
	mu      sync.Mutex
	nextID  uint64
	buckets map[string]*bucket
	uploads map[string]*upload
}

type bucket struct {
	info object.BucketInfo
	// keys holds the versions of each key, newest first.
	keys map[string][]meta.ObjectRecord
}

type upload struct {
	rec   meta.UploadRecord
	parts map[int]meta.PartRecord
}

var _ meta.Store = (*Store)(nil)

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{buckets: make(map[string]*bucket), uploads: make(map[string]*upload)}
}

// CreateBucket implements meta.Store.
func (s *Store) CreateBucket(_ context.Context, name string, opts object.MakeBucketOptions) (object.BucketInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.buckets[name]; ok {
		if b.info.Owner == opts.Owner {
			return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyOwnedByYou, "bucket already owned by you")
		}
		return object.BucketInfo{}, s3err.Errorf(s3err.BucketAlreadyExists, "bucket already exists")
	}
	s.nextID++
	info := object.BucketInfo{
		Name:       name,
		ID:         ids.BucketID(s.nextID),
		Created:    time.Now(),
		Owner:      opts.Owner,
		Versioning: opts.Versioning,
	}
	s.buckets[name] = &bucket{info: info, keys: make(map[string][]meta.ObjectRecord)}
	return info, nil
}

// GetBucket implements meta.Store.
func (s *Store) GetBucket(_ context.Context, name string) (object.BucketInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(name)
	if err != nil {
		return object.BucketInfo{}, err
	}
	return b.info, nil
}

// ListBuckets implements meta.Store.
func (s *Store) ListBuckets(_ context.Context) ([]object.BucketInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]object.BucketInfo, 0, len(s.buckets))
	for _, b := range s.buckets {
		out = append(out, b.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// DeleteBucket implements meta.Store.
func (s *Store) DeleteBucket(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.bucketLocked(name); err != nil {
		return err
	}
	if s.bucketNonEmptyLocked(name) {
		return s3err.Errorf(s3err.BucketNotEmpty, "bucket is not empty")
	}
	delete(s.buckets, name)
	return nil
}

// SetBucketVersioning implements meta.Store.
func (s *Store) SetBucketVersioning(_ context.Context, name string, state object.VersioningState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(name)
	if err != nil {
		return err
	}
	b.info.Versioning = state
	return nil
}

// PutObject implements meta.Store.
func (s *Store) PutObject(_ context.Context, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(rec.Bucket)
	if err != nil {
		return nil, err
	}
	if err := checkVersionID(b.info.Versioning, rec.VersionID); err != nil {
		return nil, err
	}
	versions := b.keys[rec.Key]
	exists, etag := liveLocked(versions)
	if err := cond.EvaluateWrite(exists, etag); err != nil {
		return nil, err
	}
	var garbage []meta.ObjectRecord
	kept := make([]meta.ObjectRecord, 0, len(versions)+1)
	kept = append(kept, rec)
	for _, v := range versions {
		if v.VersionID == rec.VersionID && b.info.Versioning != object.VersioningEnabled {
			garbage = append(garbage, v)
			continue
		}
		kept = append(kept, v)
	}
	b.keys[rec.Key] = kept
	return garbage, nil
}

// GetObject implements meta.Store.
func (s *Store) GetObject(_ context.Context, bucketName, key, versionID string) (meta.ObjectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(bucketName)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	versions := b.keys[key]
	if len(versions) == 0 {
		return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchKey, "no such key")
	}
	if versionID == "" {
		return versions[0], nil
	}
	for _, v := range versions {
		if v.VersionID == versionID {
			return v, nil
		}
	}
	return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchVersion, "no such version")
}

// DeleteObject implements meta.Store.
func (s *Store) DeleteObject(_ context.Context, bucketName, key, versionID string, cond object.Conditions) (object.DeleteResult, []meta.ObjectRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(bucketName)
	if err != nil {
		return object.DeleteResult{}, nil, err
	}
	versions := b.keys[key]
	exists, etag := liveLocked(versions)
	if err := cond.EvaluateWrite(exists, etag); err != nil {
		return object.DeleteResult{}, nil, err
	}
	res := object.DeleteResult{Key: key, VersionID: versionID}
	var garbage []meta.ObjectRecord
	switch {
	case versionID != "":
		if i := indexOf(versions, versionID); i >= 0 {
			garbage = append(garbage, versions[i])
			versions = removeAt(versions, i)
		}
	case b.info.Versioning == object.VersioningEnabled:
		marker := newMarker(bucketName, key, ids.NewDataID().String())
		versions = append([]meta.ObjectRecord{marker}, versions...)
		res.DeleteMarker = true
		res.DeleteMarkerVersionID = marker.VersionID
	default:
		if i := indexOf(versions, ""); i >= 0 {
			garbage = append(garbage, versions[i])
			versions = removeAt(versions, i)
		}
		if b.info.Versioning == object.VersioningSuspended {
			versions = append([]meta.ObjectRecord{newMarker(bucketName, key, "")}, versions...)
			res.DeleteMarker = true
		}
	}
	if len(versions) == 0 {
		delete(b.keys, key)
	} else {
		b.keys[key] = versions
	}
	return res, garbage, nil
}

// ListObjects implements meta.Store.
func (s *Store) ListObjects(_ context.Context, bucketName string, opts object.ListOptions) (meta.ListResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(bucketName)
	if err != nil {
		return meta.ListResult{}, err
	}
	keys := make([]string, 0, len(b.keys))
	for k := range b.keys {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	max := opts.MaxKeys
	if max <= 0 {
		max = object.MaxListKeys
	}
	var res meta.ListResult
	var lastCP string
	count := 0
	for _, k := range keys {
		if !strings.HasPrefix(k, opts.Prefix) || k <= opts.After {
			continue
		}
		if opts.Delimiter != "" {
			rest := k[len(opts.Prefix):]
			if i := strings.Index(rest, opts.Delimiter); i >= 0 {
				cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
				if cp == lastCP || strings.HasPrefix(opts.After, cp) {
					continue
				}
				if count == max {
					res.IsTruncated = true
					break
				}
				res.CommonPrefixes = append(res.CommonPrefixes, cp)
				res.NextAfter, lastCP = cp, cp
				count++
				continue
			}
		}
		live, ok := liveRecord(b.keys[k])
		if !ok {
			continue
		}
		if count == max {
			res.IsTruncated = true
			break
		}
		res.Objects = append(res.Objects, live)
		res.NextAfter = k
		count++
	}
	return res, nil
}

// UpdateLayout implements meta.Store.
func (s *Store) UpdateLayout(_ context.Context, bucketName, key, versionID string, partNumber int, data ids.DataID, layout erasure.Layout) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(bucketName)
	if err != nil {
		return err
	}
	versions := b.keys[key]
	for i, v := range versions {
		if versionID != "" && v.VersionID != versionID {
			continue
		}
		for j, p := range v.Parts {
			if p.Number != partNumber || p.Data != data {
				continue
			}
			parts := append([]meta.PartRecord(nil), v.Parts...)
			parts[j].Layout = layout
			versions[i].Parts = parts
			return nil
		}
		break
	}
	return meta.ErrRevisionMismatch
}

// UpdateTags implements meta.Store. The versions of a key are kept newest
// first, so the latest version is the first one.
func (s *Store) UpdateTags(_ context.Context, bucketName, key, versionID, tags string) (meta.ObjectRecord, error) {
	if _, err := object.DecodeTags(tags); err != nil {
		return meta.ObjectRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.bucketLocked(bucketName)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	versions := b.keys[key]
	i := -1
	switch {
	case versionID == "" && len(versions) > 0:
		i = 0
	case versionID != "":
		for j, v := range versions {
			if v.VersionID == versionID {
				i = j
				break
			}
		}
	}
	switch {
	case i < 0 && versionID == "":
		return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchKey, "no such key")
	case i < 0:
		return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchVersion, "no such version")
	case versions[i].DeleteMarker && versionID == "":
		return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchKey, "no such key")
	case versions[i].DeleteMarker:
		return meta.ObjectRecord{}, s3err.Errorf(s3err.MethodNotAllowed, "the requested version is a delete marker")
	}
	versions[i].Tags = tags
	rec := versions[i]
	rec.Inline = nil
	return rec, nil
}

// CreateUpload implements meta.Store.
func (s *Store) CreateUpload(_ context.Context, up meta.UploadRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.bucketLocked(up.Bucket); err != nil {
		return err
	}
	if _, ok := s.uploads[up.UploadID]; ok {
		return fmt.Errorf("testbackend: duplicate upload id %s", up.UploadID)
	}
	s.uploads[up.UploadID] = &upload{rec: up, parts: make(map[int]meta.PartRecord)}
	return nil
}

// GetUpload implements meta.Store.
func (s *Store) GetUpload(_ context.Context, bucketName, key, uploadID string) (meta.UploadRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, err := s.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return meta.UploadRecord{}, err
	}
	return up.rec, nil
}

// PutPart implements meta.Store.
func (s *Store) PutPart(_ context.Context, bucketName, key, uploadID string, part meta.PartRecord) (*meta.PartRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, err := s.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return nil, err
	}
	var garbage *meta.PartRecord
	if old, ok := up.parts[part.Number]; ok {
		garbage = &old
	}
	up.parts[part.Number] = part
	return garbage, nil
}

// ListParts implements meta.Store.
func (s *Store) ListParts(_ context.Context, bucketName, key, uploadID string, opts object.ListPartsOptions) ([]meta.PartRecord, object.ListPartsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, err := s.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	nums := make([]int, 0, len(up.parts))
	for n := range up.parts {
		if n > opts.PartNumberMarker {
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	res := object.ListPartsResult{ChecksumAlgorithm: up.rec.Options.ChecksumAlgorithm}
	if opts.MaxParts > 0 && len(nums) > opts.MaxParts {
		nums = nums[:opts.MaxParts]
		res.IsTruncated = true
	}
	parts := make([]meta.PartRecord, 0, len(nums))
	for _, n := range nums {
		p := up.parts[n]
		parts = append(parts, p)
		res.Parts = append(res.Parts, object.PartInfo{
			PartNumber: p.Number,
			Size:       p.Size,
			ETag:       p.ETag,
			ModTime:    p.ModTime.UnixNano(),
			Checksum:   p.Checksum,
		})
	}
	if len(nums) > 0 {
		res.NextPartNumberMarker = nums[len(nums)-1]
	}
	return parts, res, nil
}

// CompleteUpload implements meta.Store.
func (s *Store) CompleteUpload(_ context.Context, uploadID string, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, []meta.PartRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, err := s.uploadLocked(rec.Bucket, rec.Key, uploadID)
	if err != nil {
		return nil, nil, err
	}
	b, err := s.bucketLocked(rec.Bucket)
	if err != nil {
		return nil, nil, err
	}
	used := make(map[ids.DataID]bool, len(rec.Parts))
	for _, p := range rec.Parts {
		stored, ok := up.parts[p.Number]
		if !ok || stored.Data != p.Data {
			return nil, nil, s3err.Errorf(s3err.InvalidPart, "part %d changed", p.Number)
		}
		used[p.Data] = true
	}
	if err := checkVersionID(b.info.Versioning, rec.VersionID); err != nil {
		return nil, nil, err
	}
	versions := b.keys[rec.Key]
	exists, etag := liveLocked(versions)
	if err := cond.EvaluateWrite(exists, etag); err != nil {
		return nil, nil, err
	}
	var garbage []meta.ObjectRecord
	kept := make([]meta.ObjectRecord, 0, len(versions)+1)
	kept = append(kept, rec)
	for _, v := range versions {
		if v.VersionID == rec.VersionID && b.info.Versioning != object.VersioningEnabled {
			garbage = append(garbage, v)
			continue
		}
		kept = append(kept, v)
	}
	b.keys[rec.Key] = kept
	var unused []meta.PartRecord
	for _, p := range up.parts {
		if !used[p.Data] {
			unused = append(unused, p)
		}
	}
	sort.Slice(unused, func(i, j int) bool { return unused[i].Number < unused[j].Number })
	delete(s.uploads, uploadID)
	return garbage, unused, nil
}

// AbortUpload implements meta.Store.
func (s *Store) AbortUpload(_ context.Context, bucketName, key, uploadID string) ([]meta.PartRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	up, err := s.uploadLocked(bucketName, key, uploadID)
	if err != nil {
		return nil, err
	}
	delete(s.uploads, uploadID)
	parts := make([]meta.PartRecord, 0, len(up.parts))
	for _, p := range up.parts {
		parts = append(parts, p)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	return parts, nil
}

// ListUploads implements meta.Store.
func (s *Store) ListUploads(_ context.Context, bucketName string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.bucketLocked(bucketName); err != nil {
		return object.ListUploadsResult{}, err
	}
	var all []meta.UploadRecord
	for _, up := range s.uploads {
		if up.rec.Bucket == bucketName {
			all = append(all, up.rec)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Key != all[j].Key {
			return all[i].Key < all[j].Key
		}
		return all[i].UploadID < all[j].UploadID
	})
	max := opts.MaxUploads
	if max <= 0 {
		max = object.MaxListKeys
	}
	var res object.ListUploadsResult
	var lastCP string
	count := 0
	for _, up := range all {
		if !strings.HasPrefix(up.Key, opts.Prefix) {
			continue
		}
		if opts.KeyMarker != "" && (up.Key < opts.KeyMarker ||
			(up.Key == opts.KeyMarker && up.UploadID <= opts.UploadIDMarker)) {
			continue
		}
		if opts.Delimiter != "" {
			rest := up.Key[len(opts.Prefix):]
			if i := strings.Index(rest, opts.Delimiter); i >= 0 {
				cp := opts.Prefix + rest[:i+len(opts.Delimiter)]
				if cp == lastCP || strings.HasPrefix(opts.KeyMarker, cp) {
					continue
				}
				if count == max {
					res.IsTruncated = true
					return res, nil
				}
				res.CommonPrefixes = append(res.CommonPrefixes, cp)
				lastCP = cp
				res.NextKeyMarker, res.NextUploadIDMarker = cp, ""
				count++
				continue
			}
		}
		if count == max {
			res.IsTruncated = true
			return res, nil
		}
		res.Uploads = append(res.Uploads, object.Upload{Key: up.Key, UploadID: up.UploadID, Initiated: up.Initiated.UnixNano()})
		res.NextKeyMarker, res.NextUploadIDMarker = up.Key, up.UploadID
		count++
	}
	return res, nil
}

func (s *Store) bucketLocked(name string) (*bucket, error) {
	b, ok := s.buckets[name]
	if !ok {
		return nil, s3err.Errorf(s3err.NoSuchBucket, "no such bucket")
	}
	return b, nil
}

func (s *Store) uploadLocked(bucketName, key, uploadID string) (*upload, error) {
	up, ok := s.uploads[uploadID]
	if !ok || up.rec.Bucket != bucketName || up.rec.Key != key {
		return nil, s3err.Errorf(s3err.NoSuchUpload, "no such upload")
	}
	return up, nil
}

func (s *Store) bucketNonEmptyLocked(name string) bool {
	if b := s.buckets[name]; len(b.keys) > 0 {
		return true
	}
	for _, up := range s.uploads {
		if up.rec.Bucket == name {
			return true
		}
	}
	return false
}

// checkVersionID enforces that version ids match the bucket versioning
// state: enabled buckets need an id, other buckets use the null version.
func checkVersionID(state object.VersioningState, versionID string) error {
	switch {
	case state == object.VersioningEnabled && versionID == "":
		return fmt.Errorf("testbackend: versioned bucket needs a version id")
	case state != object.VersioningEnabled && versionID != "":
		return fmt.Errorf("testbackend: unversioned bucket takes the null version only")
	}
	return nil
}

func liveLocked(versions []meta.ObjectRecord) (bool, string) {
	if len(versions) == 0 || versions[0].DeleteMarker {
		return false, ""
	}
	return true, versions[0].ETag
}

func liveRecord(versions []meta.ObjectRecord) (meta.ObjectRecord, bool) {
	if len(versions) == 0 || versions[0].DeleteMarker {
		return meta.ObjectRecord{}, false
	}
	return versions[0], true
}

func indexOf(versions []meta.ObjectRecord, versionID string) int {
	for i, v := range versions {
		if v.VersionID == versionID {
			return i
		}
	}
	return -1
}

func removeAt(versions []meta.ObjectRecord, i int) []meta.ObjectRecord {
	out := make([]meta.ObjectRecord, 0, len(versions)-1)
	out = append(out, versions[:i]...)
	return append(out, versions[i+1:]...)
}

func newMarker(bucketName, key, versionID string) meta.ObjectRecord {
	return meta.ObjectRecord{Bucket: bucketName, Key: key, VersionID: versionID, DeleteMarker: true, ModTime: time.Now()}
}
