package metatest

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/versionid"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// checkVersionIDLocked enforces the version id rules of a write: an enabled
// bucket needs a fresh, non-empty version id; other buckets take the null
// version id "". f.mu must be held.
func (b *bucketState) checkVersionIDLocked(vs []meta.ObjectRecord, versionID string) error {
	if b.info.Versioning == object.VersioningEnabled {
		if versionID == "" {
			return s3err.Errorf(s3err.InvalidArgument, "a write to a versioned bucket needs a version id")
		}
		if versionIndex(vs, versionID) >= 0 {
			return s3err.Errorf(s3err.InvalidArgument, "version id %q is already used by this key", versionID)
		}
		return nil
	}
	if versionID != "" {
		return s3err.Errorf(s3err.InvalidArgument, "a write to an unversioned bucket takes no version id")
	}
	return nil
}

// takeNullLocked removes the null version of key, if any, and returns it.
// f.mu must be held.
func (b *bucketState) takeNullLocked(key string) (meta.ObjectRecord, bool) {
	vs := b.objects[key]
	i := versionIndex(vs, "")
	if i < 0 {
		return meta.ObjectRecord{}, false
	}
	removed := vs[i]
	b.setVersionsLocked(key, removeAt(vs, i))
	return removed, true
}

// setVersionsLocked stores the versions of key, dropping the key when it has
// none. f.mu must be held.
func (b *bucketState) setVersionsLocked(key string, vs []meta.ObjectRecord) {
	if len(vs) == 0 {
		delete(b.objects, key)
		return
	}
	b.objects[key] = vs
}

// putVersionLocked makes rec the newest version of its key. In a bucket that
// is not enabled, rec replaces the null version, which is returned as
// garbage. f.mu must be held.
func (b *bucketState) putVersionLocked(rec meta.ObjectRecord) []meta.ObjectRecord {
	var garbage []meta.ObjectRecord
	if b.info.Versioning != object.VersioningEnabled {
		if old, ok := b.takeNullLocked(rec.Key); ok {
			garbage = []meta.ObjectRecord{cloneObject(old)}
		}
	}
	b.objects[rec.Key] = append(b.objects[rec.Key], rec)
	return garbage
}

// commitLocked validates the version id and the conditions of a write, then
// stores rec. It is shared by PutObject and CompleteUpload. f.mu must be
// held.
func (b *bucketState) commitLocked(rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	vs := b.objects[rec.Key]
	if err := b.checkVersionIDLocked(vs, rec.VersionID); err != nil {
		return nil, err
	}
	if err := writeCondition(vs, cond); err != nil {
		return nil, err
	}
	return b.putVersionLocked(cloneObject(rec)), nil
}

// PutObject implements meta.Store.
func (f *Fake) PutObject(_ context.Context, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(rec.Bucket)
	if err != nil {
		return nil, err
	}
	return b.commitLocked(rec, cond)
}

// GetObject implements meta.Store. A delete marker is returned as a record
// with DeleteMarker set when it is the requested or latest version.
func (f *Fake) GetObject(_ context.Context, bucket, key, versionID string) (meta.ObjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	vs := b.objects[key]
	if versionID == "" {
		latest, ok := latestVersion(vs)
		if !ok {
			return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchKey, "key %q does not exist", key)
		}
		return cloneObject(latest), nil
	}
	i := versionIndex(vs, versionID)
	if i < 0 {
		return meta.ObjectRecord{}, s3err.Errorf(s3err.NoSuchVersion, "version %q of key %q does not exist", versionID, key)
	}
	return cloneObject(vs[i]), nil
}

// DeleteObject implements meta.Store. The result describes the outcome: a
// version delete reports the version and whether it was a delete marker; a
// delete without a version id reports the marker it added, if any.
func (f *Fake) DeleteObject(_ context.Context, bucket, key, versionID string, cond object.Conditions) (object.DeleteResult, []meta.ObjectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return object.DeleteResult{}, nil, err
	}
	vs := b.objects[key]
	if err := writeCondition(vs, cond); err != nil {
		return object.DeleteResult{}, nil, err
	}
	res := object.DeleteResult{Key: key}

	if versionID != "" {
		res.VersionID = versionID
		i := versionIndex(vs, versionID)
		if i < 0 {
			return res, nil, nil
		}
		removed := vs[i]
		b.setVersionsLocked(key, removeAt(vs, i))
		if removed.DeleteMarker {
			res.DeleteMarker = true
			res.DeleteMarkerVersionID = versionID
		}
		return res, []meta.ObjectRecord{cloneObject(removed)}, nil
	}

	switch b.info.Versioning {
	case object.VersioningEnabled:
		id := versionid.New()
		b.objects[key] = append(vs, deleteMarker(bucket, key, id))
		res.VersionID = id
		res.DeleteMarker = true
		res.DeleteMarkerVersionID = id
		return res, nil, nil
	case object.VersioningSuspended:
		garbage := b.putVersionLocked(deleteMarker(bucket, key, ""))
		res.DeleteMarker = true
		return res, garbage, nil
	default:
		old, ok := b.takeNullLocked(key)
		if !ok {
			return res, nil, nil
		}
		return res, []meta.ObjectRecord{cloneObject(old)}, nil
	}
}

// deleteMarker returns a delete marker record.
func deleteMarker(bucket, key, versionID string) meta.ObjectRecord {
	return meta.ObjectRecord{
		Bucket:       bucket,
		Key:          key,
		VersionID:    versionID,
		ModTime:      time.Now(),
		DeleteMarker: true,
	}
}

// listEntry is one item of a delimited listing: an object or a common prefix.
type listEntry struct {
	name   string
	object *meta.ObjectRecord // nil for a common prefix
}

// ListObjects implements meta.Store. Items are the latest non-delete-marker
// versions and the common prefixes they roll up to, in byte order.
func (f *Fake) ListObjects(_ context.Context, bucket string, opts object.ListOptions) (meta.ListResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return meta.ListResult{}, err
	}
	limit, err := pageSize(opts.MaxKeys)
	if err != nil {
		return meta.ListResult{}, err
	}
	var entries []listEntry
	seen := make(map[string]bool)
	for key, vs := range b.objects {
		if !strings.HasPrefix(key, opts.Prefix) || key <= opts.After {
			continue
		}
		latest, _ := latestVersion(vs)
		if latest.DeleteMarker {
			continue
		}
		name, isPrefix := rollup(key, opts.Prefix, opts.Delimiter)
		if !isPrefix {
			rec := cloneObject(latest)
			entries = append(entries, listEntry{name: key, object: &rec})
			continue
		}
		if name <= opts.After || seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, listEntry{name: name})
	}
	sortEntries(entries)
	return pageListing(entries, limit), nil
}

// sortEntries orders listing entries by name, which is byte order.
func sortEntries(entries []listEntry) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
}

// pageListing returns the first limit entries of a sorted listing.
func pageListing(entries []listEntry, limit int) meta.ListResult {
	var res meta.ListResult
	if len(entries) > limit {
		res.IsTruncated = true
		entries = entries[:limit]
	}
	for _, e := range entries {
		if e.object != nil {
			res.Objects = append(res.Objects, *e.object)
		} else {
			res.CommonPrefixes = append(res.CommonPrefixes, e.name)
		}
	}
	if len(entries) > 0 {
		res.NextAfter = entries[len(entries)-1].name
	}
	return res
}

// UpdateLayout implements meta.Store. The version must exist, must not be a
// delete marker, and must hold a part with the given number and data id.
func (f *Fake) UpdateLayout(_ context.Context, bucket, key, versionID string, partNumber int, data ids.DataID, layout erasure.Layout) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return err
	}
	vs := b.objects[key]
	i := len(vs) - 1
	if versionID != "" {
		i = versionIndex(vs, versionID)
	}
	if i < 0 || vs[i].DeleteMarker {
		return meta.ErrRevisionMismatch
	}
	for j := range vs[i].Parts {
		p := &vs[i].Parts[j]
		if p.Number != partNumber {
			continue
		}
		if p.Data != data {
			return meta.ErrRevisionMismatch
		}
		p.Layout = cloneLayout(layout)
		return nil
	}
	return meta.ErrRevisionMismatch
}
