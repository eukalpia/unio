package fsm

import (
	"fmt"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// Read answers a query from the committed state. Callers must have
// established linearizability first (see group.Read). Reads never change the
// state, so Read may run concurrently with Apply.
func (f *FSM) Read(q *pb.ReadQuery) (*pb.ReadResult, error) {
	f.readMu.RLock()
	defer f.readMu.RUnlock()
	if f.closed {
		return nil, ErrClosed
	}
	if err := f.failure(); err != nil {
		return nil, err
	}
	switch x := q.GetQuery().(type) {
	case *pb.ReadQuery_GetObject:
		return f.withReadKind(KindData, func() (*pb.ReadResult, error) { return f.readObject(x.GetObject) })
	case *pb.ReadQuery_GetUpload:
		return f.withReadKind(KindData, func() (*pb.ReadResult, error) { return f.readUpload(x.GetUpload) })
	case *pb.ReadQuery_ListParts:
		return f.withReadKind(KindData, func() (*pb.ReadResult, error) { return f.readParts(x.ListParts) })
	case *pb.ReadQuery_ListObjects:
		return f.withReadKind(KindData, func() (*pb.ReadResult, error) { return f.readListObjects(x.ListObjects) })
	case *pb.ReadQuery_ListUploads:
		return f.withReadKind(KindData, func() (*pb.ReadResult, error) { return f.readListUploads(x.ListUploads) })
	case *pb.ReadQuery_BucketHasData:
		return f.withReadKind(KindData, func() (*pb.ReadResult, error) { return f.readBucketHasData(x.BucketHasData) })
	case *pb.ReadQuery_GetBucket:
		return f.withReadKind(KindSystem, func() (*pb.ReadResult, error) { return f.readBucket(x.GetBucket) })
	case *pb.ReadQuery_ListBuckets:
		return f.withReadKind(KindSystem, func() (*pb.ReadResult, error) { return f.readBuckets() })
	case *pb.ReadQuery_KvGet:
		return f.withReadKind(KindSystem, func() (*pb.ReadResult, error) { return f.readKVGet(x.KvGet) })
	case *pb.ReadQuery_KvList:
		return f.withReadKind(KindSystem, func() (*pb.ReadResult, error) { return f.readKVList(x.KvList) })
	default:
		return nil, fmt.Errorf("fsm: unknown read query %T", x)
	}
}

// withReadKind runs fn when the state machine serves the query family.
func (f *FSM) withReadKind(want Kind, fn func() (*pb.ReadResult, error)) (*pb.ReadResult, error) {
	if f.kind != want {
		return nil, errWrongGroup
	}
	return fn()
}

// readObject returns one version. Delete markers are returned with
// DeleteMarker set. A missing key is NoSuchKey; a missing version is
// NoSuchVersion.
func (f *FSM) readObject(q *pb.GetObjectQuery) (*pb.ReadResult, error) {
	rec, _, err := f.resolveVersion(ids.BucketID(q.BucketId), []byte(q.Key), q.VersionId)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		if q.VersionId == "" {
			return nil, s3err.Errorf(s3err.NoSuchKey, "object does not exist")
		}
		return nil, s3err.Errorf(s3err.NoSuchVersion, "version does not exist")
	}
	return &pb.ReadResult{Result: &pb.ReadResult_Object{Object: &pb.ObjectResult{Record: rec}}}, nil
}

// readUpload returns one in-progress upload.
func (f *FSM) readUpload(q *pb.GetUploadQuery) (*pb.ReadResult, error) {
	up, err := f.uploadRecord(ids.BucketID(q.BucketId), []byte(q.Key), q.UploadId)
	if err != nil {
		return nil, err
	}
	if up == nil {
		return nil, errNoUpload
	}
	return &pb.ReadResult{Result: &pb.ReadResult_Upload{Upload: &pb.UploadResult{Upload: up}}}, nil
}

// readParts returns the parts of an upload after the marker. The result
// reports whether more parts follow the page.
func (f *FSM) readParts(q *pb.ListPartsQuery) (*pb.ReadResult, error) {
	bucket := ids.BucketID(q.BucketId)
	up, err := f.uploadRecord(bucket, []byte(q.Key), q.UploadId)
	if err != nil {
		return nil, err
	}
	if up == nil {
		return nil, errNoUpload
	}
	stored, err := f.uploadParts(bucket, q.UploadId)
	if err != nil {
		return nil, err
	}
	res := &pb.PartsResult{ChecksumAlgorithm: up.ChecksumAlgorithm}
	for _, p := range stored {
		if p.Number <= q.PartNumberMarker {
			continue
		}
		if q.Limit > 0 && uint32(len(res.Parts)) == q.Limit {
			res.More = true
			break
		}
		res.Parts = append(res.Parts, p)
	}
	return &pb.ReadResult{Result: &pb.ReadResult_Parts{Parts: res}}, nil
}

// readBucketHasData reports whether a partition holds object versions or
// uploads of a bucket. Parts exist only under uploads, so uploads suffice.
func (f *FSM) readBucketHasData(q *pb.BucketHasDataQuery) (*pb.ReadResult, error) {
	bucket := ids.BucketID(q.BucketId)
	for _, prefix := range [][]byte{keys.ObjectBucketPrefix(bucket), keys.UploadBucketPrefix(bucket)} {
		it, err := f.prefixIter(prefix)
		if err != nil {
			return nil, err
		}
		has := it.First()
		if err := closeIter(it, it.Error()); err != nil {
			return nil, err
		}
		if has {
			return &pb.ReadResult{Result: &pb.ReadResult_BucketHasData{BucketHasData: &pb.BucketHasDataResult{HasData: true}}}, nil
		}
	}
	return &pb.ReadResult{Result: &pb.ReadResult_BucketHasData{BucketHasData: &pb.BucketHasDataResult{}}}, nil
}

// readBucket returns a bucket by name. Buckets marked deleting are absent
// unless the query asks for them.
func (f *FSM) readBucket(q *pb.GetBucketQuery) (*pb.ReadResult, error) {
	rec, err := f.bucketRecord(q.Name)
	if err != nil {
		return nil, err
	}
	if rec == nil || (rec.Deleting && !q.IncludeDeleting) {
		return nil, errNoBucket
	}
	return &pb.ReadResult{Result: &pb.ReadResult_Bucket{Bucket: &pb.BucketResult{Bucket: rec}}}, nil
}

// readBuckets returns every bucket that is not being deleted, in name order.
func (f *FSM) readBuckets() (*pb.ReadResult, error) {
	it, err := f.prefixIter(keys.BucketPrefix())
	if err != nil {
		return nil, err
	}
	res := &pb.BucketsResult{}
	for ok := it.First(); ok; ok = it.Next() {
		rec := &pb.BucketRecord{}
		if err := rec.UnmarshalVT(it.Value()); err != nil {
			return nil, closeIter(it, fmt.Errorf("fsm: decode bucket: %w", err))
		}
		if !rec.Deleting {
			res.Buckets = append(res.Buckets, rec)
		}
	}
	return &pb.ReadResult{Result: &pb.ReadResult_Buckets{Buckets: res}}, closeIter(it, it.Error())
}

// readKVGet returns one system key-value entry, or meta.ErrNotFound.
func (f *FSM) readKVGet(q *pb.KVGetQuery) (*pb.ReadResult, error) {
	value, rev, found, err := f.kvRecord(q.Key)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, meta.ErrNotFound
	}
	entry := &pb.KVEntry{Key: q.Key, Value: value, Revision: rev}
	return &pb.ReadResult{Result: &pb.ReadResult_Kv{Kv: &pb.KVResult{Entries: []*pb.KVEntry{entry}}}}, nil
}

// readKVList returns every key-value entry whose key starts with the prefix.
func (f *FSM) readKVList(q *pb.KVListQuery) (*pb.ReadResult, error) {
	prefix := keys.KV([]byte(q.Prefix))
	it, err := f.prefixIter(prefix)
	if err != nil {
		return nil, err
	}
	res := &pb.KVResult{}
	for ok := it.First(); ok; ok = it.Next() {
		k, err := keys.ParseKV(it.Key())
		if err != nil {
			return nil, closeIter(it, err)
		}
		v := it.Value()
		if len(v) < 8 {
			return nil, closeIter(it, fmt.Errorf("fsm: kv record has %d bytes", len(v)))
		}
		res.Entries = append(res.Entries, &pb.KVEntry{
			Key:      string(k),
			Value:    append([]byte(nil), v[8:]...),
			Revision: beUint64(v[:8]),
		})
	}
	return &pb.ReadResult{Result: &pb.ReadResult_Kv{Kv: res}}, closeIter(it, it.Error())
}
