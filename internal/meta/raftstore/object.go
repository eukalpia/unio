package raftstore

import (
	"context"
	"fmt"
	"time"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/versionid"
	"github.com/eukalpia/unio/internal/meta/recordpb"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// PutObject implements meta.Store. In a versioned bucket rec.VersionID must
// be a new id, which the caller chooses (see versionid.New).
func (s *Store) PutObject(ctx context.Context, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	if err := object.ValidateKey(rec.Key); err != nil {
		return nil, err
	}
	info, _, err := s.lookupBucket(ctx, rec.Bucket, false)
	if err != nil {
		return nil, err
	}
	orec, err := recordpb.ObjectToProto(rec)
	if err != nil {
		return nil, fmt.Errorf("raftstore: encode object: %w", err)
	}
	cmd := &pb.Command{Cmd: &pb.Command_PutObject{PutObject: &pb.PutObject{
		BucketId:   uint64(info.ID),
		Record:     orec,
		Conditions: conditionsToProto(cond),
	}}}
	res, err := s.router.Propose(ctx, s.partitionOf(info.ID, rec.Key), cmd)
	if err != nil {
		return nil, err
	}
	return decodeObjects(res.GetGarbage())
}

// GetObject implements meta.Store. Delete markers are returned with
// DeleteMarker set.
func (s *Store) GetObject(ctx context.Context, bucket, key, versionID string) (meta.ObjectRecord, error) {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	q := &pb.ReadQuery{Query: &pb.ReadQuery_GetObject{GetObject: &pb.GetObjectQuery{
		BucketId:  uint64(info.ID),
		Bucket:    bucket,
		Key:       key,
		VersionId: versionID,
	}}}
	res, err := s.router.Read(ctx, s.partitionOf(info.ID, key), q)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	rec, err := recordpb.ObjectFromProto(res.GetObject().GetRecord())
	if err != nil {
		return meta.ObjectRecord{}, fmt.Errorf("raftstore: decode object: %w", err)
	}
	return rec, nil
}

// DeleteObject implements meta.Store. A delete without a version id adds a
// delete marker in a versioned bucket; the marker's id is generated here and
// returned in the result.
func (s *Store) DeleteObject(ctx context.Context, bucket, key, versionID string, cond object.Conditions) (object.DeleteResult, []meta.ObjectRecord, error) {
	if err := object.ValidateKey(key); err != nil {
		return object.DeleteResult{}, nil, err
	}
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return object.DeleteResult{}, nil, err
	}
	markerID := ""
	if versionID == "" {
		markerID = versionid.New()
	}
	cmd := &pb.Command{Cmd: &pb.Command_DeleteObject{DeleteObject: &pb.DeleteObject{
		BucketId:        uint64(info.ID),
		Bucket:          bucket,
		Key:             key,
		VersionId:       versionID,
		Conditions:      conditionsToProto(cond),
		ModTimeUnixNano: time.Now().UnixNano(),
		MarkerVersionId: markerID,
	}}}
	res, err := s.router.Propose(ctx, s.partitionOf(info.ID, key), cmd)
	if err != nil {
		return object.DeleteResult{}, nil, err
	}
	garbage, err := decodeObjects(res.GetGarbage())
	if err != nil {
		return object.DeleteResult{}, nil, err
	}
	result := object.DeleteResult{
		Key:                   key,
		VersionID:             versionID,
		DeleteMarker:          res.GetDeleteMarker(),
		DeleteMarkerVersionID: res.GetDeleteMarkerVersionId(),
	}
	if versionID == "" && result.DeleteMarkerVersionID != "" {
		// A delete that adds a marker reports the marker as the version.
		result.VersionID = result.DeleteMarkerVersionID
	}
	return result, garbage, nil
}

// UpdateLayout implements meta.Store.
func (s *Store) UpdateLayout(ctx context.Context, bucket, key, versionID string, partNumber int, data ids.DataID, layout erasure.Layout) error {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return err
	}
	if partNumber < 0 || partNumber > 1<<32-1 {
		return fmt.Errorf("raftstore: part number %d out of range", partNumber)
	}
	pl, err := recordpb.LayoutToProto(layout)
	if err != nil {
		return fmt.Errorf("raftstore: encode layout: %w", err)
	}
	cmd := &pb.Command{Cmd: &pb.Command_UpdateLayout{UpdateLayout: &pb.UpdateLayout{
		BucketId:   uint64(info.ID),
		Key:        key,
		VersionId:  versionID,
		PartNumber: uint32(partNumber),
		Data:       append([]byte(nil), data[:]...),
		Layout:     pl,
	}}}
	_, err = s.router.Propose(ctx, s.partitionOf(info.ID, key), cmd)
	return err
}

// UpdateTags implements meta.Store. The state machine checks the tag set and
// the target version, and the reply is the record it wrote.
func (s *Store) UpdateTags(ctx context.Context, bucket, key, versionID, tags string) (meta.ObjectRecord, error) {
	if err := object.ValidateKey(key); err != nil {
		return meta.ObjectRecord{}, err
	}
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	cmd := &pb.Command{Cmd: &pb.Command_UpdateTags{UpdateTags: &pb.UpdateTags{
		BucketId:  uint64(info.ID),
		Key:       key,
		VersionId: versionID,
		Tags:      tags,
	}}}
	res, err := s.router.Propose(ctx, s.partitionOf(info.ID, key), cmd)
	if err != nil {
		return meta.ObjectRecord{}, err
	}
	rec, err := recordpb.ObjectFromProto(res.GetObject())
	if err != nil {
		return meta.ObjectRecord{}, fmt.Errorf("raftstore: decode object: %w", err)
	}
	return rec, nil
}

// conditionsToProto keeps the preconditions a write evaluates.
func conditionsToProto(c object.Conditions) *pb.WriteConditions {
	return &pb.WriteConditions{IfMatch: c.IfMatch, IfNoneMatch: c.IfNoneMatch}
}

// decodeObjects converts stored object records to domain records.
func decodeObjects(in []*pb.ObjectRecord) ([]meta.ObjectRecord, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]meta.ObjectRecord, 0, len(in))
	for _, p := range in {
		rec, err := recordpb.ObjectFromProto(p)
		if err != nil {
			return nil, fmt.Errorf("raftstore: decode object: %w", err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// decodeParts converts stored part records to domain records.
func decodeParts(in []*pb.PartRecord) ([]meta.PartRecord, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]meta.PartRecord, 0, len(in))
	for _, p := range in {
		part, err := recordpb.PartFromProto(p)
		if err != nil {
			return nil, fmt.Errorf("raftstore: decode part: %w", err)
		}
		out = append(out, part)
	}
	return out, nil
}
