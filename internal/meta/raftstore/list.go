package raftstore

import (
	"context"
	"fmt"
	"math"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/listing"
	"github.com/eukalpia/unio/internal/meta/recordpb"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// ListObjects implements meta.Store. Every data partition is scanned in
// parallel and the sorted streams are merged (see package listing).
func (s *Store) ListObjects(ctx context.Context, bucket string, opts object.ListOptions) (meta.ListResult, error) {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return meta.ListResult{}, err
	}
	fetch := func(ctx context.Context, p ids.PartitionID, cursor string, limit int) (listing.Batch, error) {
		after := cursor
		if cursor == "" {
			after = opts.After
		}
		pageSize, err := uint32Limit(limit)
		if err != nil {
			return listing.Batch{}, err
		}
		q := &pb.ReadQuery{Query: &pb.ReadQuery_ListObjects{ListObjects: &pb.ListObjectsQuery{
			BucketId:  uint64(info.ID),
			Prefix:    []byte(opts.Prefix),
			Delimiter: []byte(opts.Delimiter),
			After:     []byte(after),
			Limit:     pageSize,
		}}}
		res, err := s.router.Read(ctx, p, q)
		if err != nil {
			return listing.Batch{}, err
		}
		batch := listing.Batch{Exhausted: res.GetListObjects().GetExhausted()}
		for _, it := range res.GetListObjects().GetItems() {
			if obj := it.GetObject(); obj != nil {
				rec, err := recordpb.ObjectFromProto(obj)
				if err != nil {
					return listing.Batch{}, fmt.Errorf("raftstore: decode listed object: %w", err)
				}
				batch.Items = append(batch.Items, listing.Item{Sort: rec.Key, Object: rec})
				continue
			}
			cp := it.GetCommonPrefix()
			batch.Items = append(batch.Items, listing.Item{Sort: cp, CommonPrefix: true})
		}
		return batch, nil
	}
	return listing.ListObjects(ctx, s.dataParts, fetch, opts)
}

// ListUploads implements meta.Store. Uploads are merged across partitions
// in (key, upload id) order.
func (s *Store) ListUploads(ctx context.Context, bucket string, opts object.ListUploadsOptions) (object.ListUploadsResult, error) {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return object.ListUploadsResult{}, err
	}
	fetch := func(ctx context.Context, p ids.PartitionID, cursor string, limit int) (listing.Batch, error) {
		keyMarker, uploadIDMarker := opts.KeyMarker, opts.UploadIDMarker
		if cursor != "" {
			keyMarker, uploadIDMarker = listing.SplitUploadCursor(cursor)
		}
		pageSize, err := uint32Limit(limit)
		if err != nil {
			return listing.Batch{}, err
		}
		q := &pb.ReadQuery{Query: &pb.ReadQuery_ListUploads{ListUploads: &pb.ListUploadsQuery{
			BucketId:       uint64(info.ID),
			Prefix:         []byte(opts.Prefix),
			Delimiter:      []byte(opts.Delimiter),
			KeyMarker:      keyMarker,
			UploadIdMarker: uploadIDMarker,
			Limit:          pageSize,
		}}}
		res, err := s.router.Read(ctx, p, q)
		if err != nil {
			return listing.Batch{}, err
		}
		batch := listing.Batch{Exhausted: res.GetListUploads().GetExhausted()}
		for _, it := range res.GetListUploads().GetItems() {
			if up := it.GetUpload(); up != nil {
				rec, err := recordpb.UploadFromProto(up)
				if err != nil {
					return listing.Batch{}, fmt.Errorf("raftstore: decode listed upload: %w", err)
				}
				batch.Items = append(batch.Items, listing.Item{Sort: listing.UploadCursor(rec.Key, rec.UploadID), Upload: rec})
				continue
			}
			cp := it.GetCommonPrefix()
			batch.Items = append(batch.Items, listing.Item{Sort: cp, CommonPrefix: true})
		}
		return batch, nil
	}
	return listing.ListUploads(ctx, s.dataParts, fetch, opts)
}

// uint32Limit converts a page size to the 4-byte limit field of a query. It
// fails when the size is negative or does not fit, so the conversion never
// wraps. The listing already bounds page sizes; this check guards the
// conversion itself.
func uint32Limit(n int) (uint32, error) {
	if n < 0 || int64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("raftstore: page size %d does not fit a query limit", n)
	}
	return uint32(n), nil
}
