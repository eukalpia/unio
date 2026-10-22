// Package listing merges the per-partition listings of a bucket into the
// order and paging of ListObjects and ListMultipartUploads.
//
// A bucket's keys are spread over metadata partitions. The caller supplies a
// Fetch that reads the next batch of one partition, already filtered by
// prefix and delimiter. ListObjects and ListUploads fetch the first batch of
// every partition in parallel, merge the partitions' sorted streams lazily,
// emit each common prefix once even when several partitions report it, and
// cut the merged stream into pages.
//
// Cursors. A Fetch returns the items strictly after its cursor. The first
// fetch of a listing uses the listing's start cursor; later fetches use the
// Sort of the last item of the partition's previous batch.
//
// For objects and common prefixes the cursor is the Sort itself: the key, or
// the prefix. The store adapter passes it as the partition query's After,
// which has the same exclusive meaning.
//
// For uploads the Sort of an upload is UploadCursor(key, uploadID), and the
// Sort of a common prefix is the prefix. The store adapter turns a cursor into
// the partition query's KeyMarker and UploadIDMarker with SplitUploadCursor.
// ListUploads starts from UploadCursor(opts.KeyMarker, opts.UploadIDMarker)
// when KeyMarker is set. An upload-id marker without a key marker is ignored,
// as the partition query ignores it.
package listing

import (
	"context"
	"fmt"
	"strings"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// Item is one entry of a partition's listing: an object or a common prefix.
type Item struct {
	// Sort is the entry's position in the listing order: the key for an
	// object, the prefix for a common prefix, and UploadCursor(key, uploadID)
	// for an upload.
	Sort string
	// CommonPrefix reports whether the entry is a common prefix. Otherwise
	// Object is set for an object, and Upload for an upload.
	CommonPrefix bool
	Object       meta.ObjectRecord
	Upload       meta.UploadRecord
}

// Batch is one partition's answer to a Fetch.
type Batch struct {
	// Items are strictly after the cursor, in ascending Sort order.
	Items []Item
	// Exhausted reports that the partition has no item after Items. An empty
	// batch must be exhausted.
	Exhausted bool
}

// Fetch returns the next batch of partition p: at most limit items strictly
// after cursor, where the empty cursor means the start of the partition.
// Fetch is called concurrently for different partitions and must honour ctx.
type Fetch func(ctx context.Context, p ids.PartitionID, cursor string, limit int) (Batch, error)

// UploadCursor returns the Sort of an upload, which is also its cursor: the
// key, a NUL byte, then the upload id. Keys never contain NUL, so the first
// NUL separates the two parts, and cursors sort in (key, uploadID) order.
func UploadCursor(key, uploadID string) string {
	return key + "\x00" + uploadID
}

// SplitUploadCursor is the inverse of UploadCursor. It returns the KeyMarker
// and UploadIDMarker of the partition query for a cursor. A cursor without
// NUL is a common prefix P. It splits to (P, ""), the marker that resumes
// after P, including the keys grouped under P.
func SplitUploadCursor(cursor string) (keyMarker, uploadIDMarker string) {
	i := strings.IndexByte(cursor, 0)
	if i < 0 {
		return cursor, ""
	}
	return cursor[:i], cursor[i+1:]
}

// ListObjects lists the objects and common prefixes of a bucket across the
// given partitions, in key order, from the entry after opts.After, holding at
// most opts.MaxKeys entries. Zero means object.MaxListKeys. Objects and
// common prefixes both count toward MaxKeys. The Prefix and Delimiter in opts
// are applied by the fetch; ListObjects only merges.
//
// IsTruncated reports whether an entry follows the page. NextAfter is the Sort
// of the last entry returned, and "" for an empty page.
func ListObjects(ctx context.Context, parts []ids.PartitionID, fetch Fetch, opts object.ListOptions) (meta.ListResult, error) {
	maxKeys, err := pageSize(opts.MaxKeys)
	if err != nil {
		return meta.ListResult{}, err
	}
	m, err := newMerger(ctx, parts, fetch, batchLimit(maxKeys), opts.After)
	if err != nil {
		return meta.ListResult{}, wrapErr(ctx, err)
	}
	var res meta.ListResult
	truncated, last, err := m.page(maxKeys, func(it *Item) {
		if it.CommonPrefix {
			res.CommonPrefixes = append(res.CommonPrefixes, it.Sort)
		} else {
			res.Objects = append(res.Objects, it.Object)
		}
	})
	if err != nil {
		return meta.ListResult{}, wrapErr(ctx, err)
	}
	res.IsTruncated = truncated
	if last != nil {
		res.NextAfter = last.Sort
	}
	return res, nil
}

// ListUploads lists the in-progress uploads and common prefixes of a bucket
// across the given partitions, in (key, upload id) order, from the entry after
// (opts.KeyMarker, opts.UploadIDMarker), holding at most opts.MaxUploads
// entries. Zero means object.MaxListKeys. Uploads and common prefixes both
// count toward MaxUploads.
//
// NextKeyMarker and NextUploadIDMarker name the last entry returned. For an
// upload they are its key and upload id. For a common prefix NextKeyMarker is
// the prefix and NextUploadIDMarker is "". Both are "" for an empty page.
func ListUploads(ctx context.Context, parts []ids.PartitionID, fetch Fetch, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	maxUploads, err := pageSize(opts.MaxUploads)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	var start string
	if opts.KeyMarker != "" {
		start = UploadCursor(opts.KeyMarker, opts.UploadIDMarker)
	}
	m, err := newMerger(ctx, parts, fetch, batchLimit(maxUploads), start)
	if err != nil {
		return object.ListUploadsResult{}, wrapErr(ctx, err)
	}
	var res object.ListUploadsResult
	truncated, last, err := m.page(maxUploads, func(it *Item) {
		if it.CommonPrefix {
			res.CommonPrefixes = append(res.CommonPrefixes, it.Sort)
			return
		}
		res.Uploads = append(res.Uploads, object.Upload{
			Key:       it.Upload.Key,
			UploadID:  it.Upload.UploadID,
			Initiated: it.Upload.Initiated.UnixNano(),
		})
	})
	if err != nil {
		return object.ListUploadsResult{}, wrapErr(ctx, err)
	}
	res.IsTruncated = truncated
	if last != nil {
		if last.CommonPrefix {
			res.NextKeyMarker = last.Sort
		} else {
			res.NextKeyMarker = last.Upload.Key
			res.NextUploadIDMarker = last.Upload.UploadID
		}
	}
	return res, nil
}

// pageSize resolves a requested page size: zero means the maximum, and any
// size outside 1..MaxListKeys is an InvalidArgument error.
func pageSize(n int) (int, error) {
	switch {
	case n == 0:
		return object.MaxListKeys, nil
	case n < 0 || n > object.MaxListKeys:
		return 0, s3err.Errorf(s3err.InvalidArgument, "page size must be between 1 and %d, got %d", object.MaxListKeys, n)
	default:
		return n, nil
	}
}

// wrapErr prefixes err with "listing: ". Cancellation takes precedence: the
// error then wraps ctx.Err().
func wrapErr(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil {
		err = cerr
	}
	return fmt.Errorf("listing: %w", err)
}
