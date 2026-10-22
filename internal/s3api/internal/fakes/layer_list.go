package fakes

import (
	"context"
	"maps"
	"slices"
	"strings"

	"github.com/eukalpia/unio/internal/object"
)

// ListObjects returns one page of the objects in bucketName. Entries are keys
// and common prefixes in ascending order, restricted to those strictly after
// opts.After. A key that contains opts.Delimiter after opts.Prefix rolls up
// into its common prefix, and every key of that group is reported once.
// MaxKeys counts keys and common prefixes together.
func (l *Layer) ListObjects(ctx context.Context, bucketName string, opts object.ListOptions) (object.ListResult, error) {
	if err := ctx.Err(); err != nil {
		return object.ListResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.ListResult{}, err
	}
	max, err := pageSize(opts.MaxKeys)
	if err != nil {
		return object.ListResult{}, err
	}

	var res object.ListResult
	count := 0
	lastPrefix := ""
	for _, key := range slices.Sorted(maps.Keys(b.objects)) {
		if !strings.HasPrefix(key, opts.Prefix) {
			continue
		}
		name := key
		cp, rolled := commonPrefix(key, opts.Prefix, opts.Delimiter)
		if rolled {
			name = cp
		}
		if name <= opts.After {
			continue
		}
		if rolled && cp == lastPrefix {
			continue
		}
		if count == max {
			res.IsTruncated = true
			break
		}
		if rolled {
			res.CommonPrefixes = append(res.CommonPrefixes, cp)
			lastPrefix = cp
		} else {
			res.Objects = append(res.Objects, cloneInfo(b.objects[key].info))
		}
		res.NextAfter = name
		count++
	}
	return res, nil
}

// ListMultipartUploads returns one page of the uploads in bucketName, ordered
// by key and then upload ID. The page starts strictly after the pair
// (opts.KeyMarker, opts.UploadIDMarker). A common prefix group is reported
// once, and a group that started on an earlier page is not repeated. MaxUploads
// counts uploads and common prefixes together.
func (l *Layer) ListMultipartUploads(ctx context.Context, bucketName string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	if err := ctx.Err(); err != nil {
		return object.ListUploadsResult{}, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, err := l.bucketLocked(bucketName)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	max, err := pageSize(opts.MaxUploads)
	if err != nil {
		return object.ListUploadsResult{}, err
	}

	uploads := make([]*upload, 0, len(b.uploads))
	for _, u := range b.uploads {
		uploads = append(uploads, u)
	}
	slices.SortFunc(uploads, compareUploads)

	var res object.ListUploadsResult
	count := 0
	lastPrefix := ""
	for _, u := range uploads {
		if !strings.HasPrefix(u.key, opts.Prefix) {
			continue
		}
		if cp, rolled := commonPrefix(u.key, opts.Prefix, opts.Delimiter); rolled {
			// Every key of a group sorts after the group's common prefix, so
			// a prefix at or before the key marker was already reported.
			if cp <= opts.KeyMarker || cp == lastPrefix {
				continue
			}
			if count == max {
				res.IsTruncated = true
				break
			}
			res.CommonPrefixes = append(res.CommonPrefixes, cp)
			lastPrefix = cp
			res.NextKeyMarker, res.NextUploadIDMarker = cp, ""
		} else {
			if !afterUploadMarker(u, opts.KeyMarker, opts.UploadIDMarker) {
				continue
			}
			if count == max {
				res.IsTruncated = true
				break
			}
			res.Uploads = append(res.Uploads, object.Upload{Key: u.key, UploadID: u.id, Initiated: u.initiated})
			res.NextKeyMarker, res.NextUploadIDMarker = u.key, u.id
		}
		count++
	}
	if !res.IsTruncated {
		res.NextKeyMarker, res.NextUploadIDMarker = "", ""
	}
	return res, nil
}

// commonPrefix returns the common prefix that key rolls up into when listing
// with prefix and delimiter. It returns false when key is listed as itself.
// key must start with prefix.
func commonPrefix(key, prefix, delimiter string) (string, bool) {
	if delimiter == "" {
		return "", false
	}
	rest := key[len(prefix):]
	i := strings.Index(rest, delimiter)
	if i < 0 {
		return "", false
	}
	return prefix + rest[:i+len(delimiter)], true
}

// compareUploads orders uploads by key and then by upload ID.
func compareUploads(a, b *upload) int {
	if c := strings.Compare(a.key, b.key); c != 0 {
		return c
	}
	return strings.Compare(a.id, b.id)
}

// afterUploadMarker reports whether u sorts strictly after the marker pair.
func afterUploadMarker(u *upload, keyMarker, idMarker string) bool {
	return u.key > keyMarker || (u.key == keyMarker && u.id > idMarker)
}
