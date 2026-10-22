package fsm

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// errBadLimit is returned for listing queries without a positive limit.
var errBadLimit = errors.New("fsm: listing limit must be positive")

// seekTo positions it at the first key at or after target. A nil target
// means the end of the range, so the iterator becomes invalid.
func seekTo(it *pebble.Iterator, target []byte) bool {
	if target == nil {
		return false
	}
	return it.SeekGE(target)
}

// readListObjects returns the next batch of one partition's listing: the
// latest non-delete-marker version of each key, and common prefixes, both
// strictly after the cursor and in key order. Keys under a common prefix are
// skipped with a seek past the prefix, so the cost of a page does not grow
// with the number of keys under each prefix.
//
// Items are ordered by their string value: an object key, or a common prefix
// (always a strict prefix of the keys it groups). The cursor is the last
// item returned by the previous page, so one comparison of an item with the
// cursor decides whether it was already returned.
func (f *FSM) readListObjects(q *pb.ListObjectsQuery) (*pb.ReadResult, error) {
	if q.Limit == 0 {
		return nil, errBadLimit
	}
	bucket := ids.BucketID(q.BucketId)
	limit := int(q.Limit)
	prefix, delim, after := q.Prefix, q.Delimiter, q.After
	hasAfter := len(after) > 0

	start := keys.ObjectKeyStart(bucket, prefix)
	if hasAfter {
		if s := keys.Successor(keys.ObjectVersionPrefix(bucket, after)); s != nil && bytes.Compare(s, start) > 0 {
			start = s
		}
	}
	it, err := f.iter(start, keys.Successor(keys.ObjectBucketPrefix(bucket)))
	if err != nil {
		return nil, err
	}
	res := &pb.ListObjectsResult{Exhausted: true}
	count := 0
	ok := it.SeekGE(start)
	for ok {
		_, key, _, err := keys.ParseObjectVersion(it.Key())
		if err != nil {
			return nil, closeIter(it, err)
		}
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		if len(delim) > 0 {
			if i := bytes.Index(key[len(prefix):], delim); i >= 0 {
				cp := key[:len(prefix)+i+len(delim)]
				target := keys.ObjectKeySeekPast(bucket, cp)
				if !hasAfter || bytes.Compare(cp, after) > 0 {
					if count == limit {
						res.Exhausted = false
						break
					}
					res.Items = append(res.Items, &pb.ListItem{Item: &pb.ListItem_CommonPrefix{CommonPrefix: string(cp)}})
					count++
				}
				ok = seekTo(it, target)
				continue
			}
		}
		next := keys.Successor(keys.ObjectVersionPrefix(bucket, key))
		if hasAfter && bytes.Compare(key, after) <= 0 {
			ok = seekTo(it, next)
			continue
		}
		rec, err := unmarshalObject(it.Value())
		if err != nil {
			return nil, closeIter(it, err)
		}
		if rec.DeleteMarker {
			ok = seekTo(it, next)
			continue
		}
		if count == limit {
			res.Exhausted = false
			break
		}
		res.Items = append(res.Items, &pb.ListItem{Item: &pb.ListItem_Object{Object: rec}})
		count++
		ok = seekTo(it, next)
	}
	if err := it.Error(); err != nil {
		return nil, closeIter(it, fmt.Errorf("fsm: list objects: %w", err))
	}
	if err := closeIter(it, nil); err != nil {
		return nil, err
	}
	return &pb.ReadResult{Result: &pb.ReadResult_ListObjects{ListObjects: res}}, nil
}

// readListUploads returns the next batch of one partition's upload listing,
// ordered by (key, upload id). Common prefixes follow the same rules as
// readListObjects. The cursor is the (key marker, upload id marker) pair; an
// upload is returned only when it is strictly after the pair.
func (f *FSM) readListUploads(q *pb.ListUploadsQuery) (*pb.ReadResult, error) {
	if q.Limit == 0 {
		return nil, errBadLimit
	}
	bucket := ids.BucketID(q.BucketId)
	limit := int(q.Limit)
	prefix, delim := q.Prefix, q.Delimiter
	km, uim := q.KeyMarker, q.UploadIdMarker
	hasMarker := km != ""

	rangeStart := keys.AppendEscaped(keys.UploadBucketPrefix(bucket), prefix)
	start := rangeStart
	if hasMarker {
		if s := keys.UploadKeyPrefix(bucket, []byte(km)); bytes.Compare(s, start) > 0 {
			start = s
		}
	}
	it, err := f.iter(start, keys.Successor(keys.UploadBucketPrefix(bucket)))
	if err != nil {
		return nil, err
	}
	res := &pb.ListUploadsResult{Exhausted: true}
	count := 0
	ok := it.SeekGE(start)
	for ok {
		_, key, uploadID, err := keys.ParseUpload(it.Key())
		if err != nil {
			return nil, closeIter(it, err)
		}
		if !bytes.HasPrefix(key, prefix) {
			break
		}
		if len(delim) > 0 {
			if i := bytes.Index(key[len(prefix):], delim); i >= 0 {
				cp := key[:len(prefix)+i+len(delim)]
				target := keys.Successor(keys.AppendEscaped(keys.UploadBucketPrefix(bucket), cp))
				if !hasMarker || bytes.Compare(cp, []byte(km)) > 0 {
					if count == limit {
						res.Exhausted = false
						break
					}
					res.Items = append(res.Items, &pb.UploadListItem{Item: &pb.UploadListItem_CommonPrefix{CommonPrefix: string(cp)}})
					count++
				}
				ok = seekTo(it, target)
				continue
			}
		}
		if hasMarker {
			c := bytes.Compare(key, []byte(km))
			if c < 0 || (c == 0 && bytes.Compare(uploadID, []byte(uim)) <= 0) {
				ok = it.Next()
				continue
			}
		}
		up, err := unmarshalUpload(it.Value())
		if err != nil {
			return nil, closeIter(it, err)
		}
		if count == limit {
			res.Exhausted = false
			break
		}
		res.Items = append(res.Items, &pb.UploadListItem{Item: &pb.UploadListItem_Upload{Upload: up}})
		count++
		ok = it.Next()
	}
	if err := it.Error(); err != nil {
		return nil, closeIter(it, fmt.Errorf("fsm: list uploads: %w", err))
	}
	if err := closeIter(it, nil); err != nil {
		return nil, err
	}
	return &pb.ReadResult{Result: &pb.ReadResult_ListUploads{ListUploads: res}}, nil
}

// unmarshalUpload decodes a stored upload record.
func unmarshalUpload(v []byte) (*pb.UploadRecord, error) {
	up := &pb.UploadRecord{}
	if err := up.UnmarshalVT(v); err != nil {
		return nil, fmt.Errorf("fsm: decode upload: %w", err)
	}
	return up, nil
}
