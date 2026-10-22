package metatest

import (
	"context"
	"sort"
	"strings"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// uploadLocked returns the upload uploadID of key, or NoSuchUpload. An
// upload is only found under the key it was created for. f.mu must be held.
func (b *bucketState) uploadLocked(key, uploadID string) (*uploadState, error) {
	u, ok := b.uploads[uploadID]
	if !ok || u.rec.Key != key {
		return nil, s3err.Errorf(s3err.NoSuchUpload, "upload %q does not exist", uploadID)
	}
	return u, nil
}

// sortedParts returns the parts of u in ascending number order, as copies.
func sortedParts(u *uploadState, keep func(number int) bool) []meta.PartRecord {
	numbers := make([]int, 0, len(u.parts))
	for n := range u.parts {
		if keep(n) {
			numbers = append(numbers, n)
		}
	}
	sort.Ints(numbers)
	out := make([]meta.PartRecord, 0, len(numbers))
	for _, n := range numbers {
		out = append(out, clonePart(u.parts[n]))
	}
	return out
}

// CreateUpload implements meta.Store. The upload id must be non-empty and
// unique within the bucket.
func (f *Fake) CreateUpload(_ context.Context, up meta.UploadRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(up.Bucket)
	if err != nil {
		return err
	}
	if up.UploadID == "" {
		return s3err.Errorf(s3err.InvalidArgument, "upload id must not be empty")
	}
	if _, dup := b.uploads[up.UploadID]; dup {
		return s3err.Errorf(s3err.InvalidArgument, "upload id %q is already in use", up.UploadID)
	}
	b.uploads[up.UploadID] = &uploadState{
		rec:   cloneUpload(up),
		parts: make(map[int]meta.PartRecord),
	}
	return nil
}

// GetUpload implements meta.Store.
func (f *Fake) GetUpload(_ context.Context, bucket, key, uploadID string) (meta.UploadRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return meta.UploadRecord{}, err
	}
	u, err := b.uploadLocked(key, uploadID)
	if err != nil {
		return meta.UploadRecord{}, err
	}
	return cloneUpload(u.rec), nil
}

// PutPart implements meta.Store. A part with the same number is replaced and
// returned as garbage.
func (f *Fake) PutPart(_ context.Context, bucket, key, uploadID string, part meta.PartRecord) (*meta.PartRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}
	u, err := b.uploadLocked(key, uploadID)
	if err != nil {
		return nil, err
	}
	old, replaced := u.parts[part.Number]
	u.parts[part.Number] = clonePart(part)
	if !replaced {
		return nil, nil
	}
	garbage := clonePart(old)
	return &garbage, nil
}

// ListParts implements meta.Store. Parts are returned in ascending number
// order, strictly after PartNumberMarker, at most MaxParts of them.
func (f *Fake) ListParts(_ context.Context, bucket, key, uploadID string, opts object.ListPartsOptions) ([]meta.PartRecord, object.ListPartsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	u, err := b.uploadLocked(key, uploadID)
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	limit, err := pageSize(opts.MaxParts)
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	all := sortedParts(u, func(n int) bool { return n > opts.PartNumberMarker })
	res := object.ListPartsResult{
		ChecksumAlgorithm:    u.rec.Options.ChecksumAlgorithm,
		NextPartNumberMarker: opts.PartNumberMarker,
	}
	if len(all) > limit {
		res.IsTruncated = true
		all = all[:limit]
	}
	for _, p := range all {
		res.Parts = append(res.Parts, object.PartInfo{
			PartNumber: p.Number,
			Size:       p.Size,
			ETag:       p.ETag,
			ModTime:    p.ModTime.UnixNano(),
			Checksum:   p.Checksum,
		})
	}
	if len(all) > 0 {
		res.NextPartNumberMarker = all[len(all)-1].Number
	}
	return all, res, nil
}

// CompleteUpload implements meta.Store. Parts are checked in order, then the
// version id and the conditions are checked, and only then is the object
// committed and the upload removed. A failed call leaves the upload in place.
func (f *Fake) CompleteUpload(_ context.Context, uploadID string, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, []meta.PartRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(rec.Bucket)
	if err != nil {
		return nil, nil, err
	}
	u, err := b.uploadLocked(rec.Key, uploadID)
	if err != nil {
		return nil, nil, err
	}
	for i := 1; i < len(rec.Parts); i++ {
		if rec.Parts[i].Number <= rec.Parts[i-1].Number {
			return nil, nil, s3err.Errorf(s3err.InvalidPartOrder, "part numbers must be strictly ascending")
		}
	}
	used := make(map[int]bool, len(rec.Parts))
	parts := make([]meta.PartRecord, 0, len(rec.Parts))
	for _, p := range rec.Parts {
		stored, ok := u.parts[p.Number]
		if !ok || stored.ETag != p.ETag || stored.Data != p.Data {
			return nil, nil, s3err.Errorf(s3err.InvalidPart, "part %d does not match the uploaded part", p.Number)
		}
		used[p.Number] = true
		parts = append(parts, clonePart(stored))
	}
	unused := sortedParts(u, func(n int) bool { return !used[n] })

	obj := cloneObject(rec)
	obj.Parts = parts
	garbage, err := b.commitLocked(obj, cond)
	if err != nil {
		return nil, nil, err
	}
	delete(b.uploads, uploadID)
	return garbage, unused, nil
}

// AbortUpload implements meta.Store. It removes the upload and returns all of
// its parts.
func (f *Fake) AbortUpload(_ context.Context, bucket, key, uploadID string) ([]meta.PartRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return nil, err
	}
	u, err := b.uploadLocked(key, uploadID)
	if err != nil {
		return nil, err
	}
	parts := sortedParts(u, func(int) bool { return true })
	delete(b.uploads, uploadID)
	return parts, nil
}

// uploadEntry is one item of an upload listing: an upload, or a common
// prefix when upload is nil.
type uploadEntry struct {
	key    string
	id     string
	upload *uploadState
}

// ListUploads implements meta.Store. Uploads are ordered by (key, upload id)
// and grouped by Delimiter like ListObjects. The page continues after the
// (KeyMarker, UploadIDMarker) pair, and a common prefix is positioned at
// (prefix, "").
func (f *Fake) ListUploads(_ context.Context, bucket string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, err := f.bucketLocked(bucket)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	limit, err := pageSize(opts.MaxUploads)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	var entries []uploadEntry
	seen := make(map[string]bool)
	for id, u := range b.uploads {
		key := u.rec.Key
		if !strings.HasPrefix(key, opts.Prefix) {
			continue
		}
		name, isPrefix := rollup(key, opts.Prefix, opts.Delimiter)
		if !isPrefix {
			entries = append(entries, uploadEntry{key: key, id: id, upload: u})
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		entries = append(entries, uploadEntry{key: name})
	}
	after := func(e uploadEntry) bool {
		return e.key > opts.KeyMarker || (e.key == opts.KeyMarker && e.id > opts.UploadIDMarker)
	}
	kept := entries[:0]
	for _, e := range entries {
		if after(e) {
			kept = append(kept, e)
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].key != kept[j].key {
			return kept[i].key < kept[j].key
		}
		return kept[i].id < kept[j].id
	})
	return pageUploads(kept, limit), nil
}

// pageUploads returns the first limit entries of a sorted upload listing.
func pageUploads(entries []uploadEntry, limit int) object.ListUploadsResult {
	var res object.ListUploadsResult
	if len(entries) > limit {
		res.IsTruncated = true
		entries = entries[:limit]
	}
	for _, e := range entries {
		if e.upload == nil {
			res.CommonPrefixes = append(res.CommonPrefixes, e.key)
			continue
		}
		res.Uploads = append(res.Uploads, object.Upload{
			Key:       e.key,
			UploadID:  e.id,
			Initiated: e.upload.rec.Initiated.UnixNano(),
		})
	}
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		res.NextKeyMarker = last.key
		res.NextUploadIDMarker = last.id
	}
	return res
}
