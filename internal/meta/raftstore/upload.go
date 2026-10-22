package raftstore

import (
	"context"
	"fmt"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/recordpb"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxPartsPerPage is the largest page ListParts returns.
const maxPartsPerPage = object.MaxListKeys

// CreateUpload implements meta.Store. up.UploadID must be unique; the caller
// generates it.
func (s *Store) CreateUpload(ctx context.Context, up meta.UploadRecord) error {
	if err := object.ValidateKey(up.Key); err != nil {
		return err
	}
	info, _, err := s.lookupBucket(ctx, up.Bucket, false)
	if err != nil {
		return err
	}
	pu, err := recordpb.UploadToProto(up)
	if err != nil {
		return fmt.Errorf("raftstore: encode upload: %w", err)
	}
	cmd := &pb.Command{Cmd: &pb.Command_CreateUpload{CreateUpload: &pb.CreateUpload{
		BucketId: uint64(info.ID),
		Upload:   pu,
	}}}
	_, err = s.router.Propose(ctx, s.partitionOf(info.ID, up.Key), cmd)
	return err
}

// GetUpload implements meta.Store.
func (s *Store) GetUpload(ctx context.Context, bucket, key, uploadID string) (meta.UploadRecord, error) {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return meta.UploadRecord{}, err
	}
	q := &pb.ReadQuery{Query: &pb.ReadQuery_GetUpload{GetUpload: &pb.GetUploadQuery{
		BucketId: uint64(info.ID),
		Key:      key,
		UploadId: uploadID,
	}}}
	res, err := s.router.Read(ctx, s.partitionOf(info.ID, key), q)
	if err != nil {
		return meta.UploadRecord{}, err
	}
	up, err := recordpb.UploadFromProto(res.GetUpload().GetUpload())
	if err != nil {
		return meta.UploadRecord{}, fmt.Errorf("raftstore: decode upload: %w", err)
	}
	return up, nil
}

// PutPart implements meta.Store. It returns the part it replaced, if any.
func (s *Store) PutPart(ctx context.Context, bucket, key, uploadID string, part meta.PartRecord) (*meta.PartRecord, error) {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return nil, err
	}
	pp, err := recordpb.PartToProto(part)
	if err != nil {
		return nil, fmt.Errorf("raftstore: encode part: %w", err)
	}
	cmd := &pb.Command{Cmd: &pb.Command_PutPart{PutPart: &pb.PutPart{
		BucketId: uint64(info.ID),
		Key:      key,
		UploadId: uploadID,
		Part:     pp,
	}}}
	res, err := s.router.Propose(ctx, s.partitionOf(info.ID, key), cmd)
	if err != nil {
		return nil, err
	}
	if res.GetReplacedPart() == nil {
		return nil, nil
	}
	replaced, err := recordpb.PartFromProto(res.GetReplacedPart())
	if err != nil {
		return nil, fmt.Errorf("raftstore: decode part: %w", err)
	}
	return &replaced, nil
}

// ListParts implements meta.Store. The parts are returned as the first
// result; the ListPartsResult carries the paging state and the upload's
// checksum algorithm, and leaves Parts empty.
func (s *Store) ListParts(ctx context.Context, bucket, key, uploadID string, opts object.ListPartsOptions) ([]meta.PartRecord, object.ListPartsResult, error) {
	maxParts := opts.MaxParts
	switch {
	case maxParts == 0:
		maxParts = maxPartsPerPage
	case maxParts < 0 || maxParts > maxPartsPerPage:
		return nil, object.ListPartsResult{}, s3err.Errorf(s3err.InvalidArgument, "max parts must be between 1 and %d", maxPartsPerPage)
	}
	if opts.PartNumberMarker < 0 || opts.PartNumberMarker > object.MaxPartNumber {
		return nil, object.ListPartsResult{}, s3err.Errorf(s3err.InvalidArgument, "part number marker out of range")
	}
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	q := &pb.ReadQuery{Query: &pb.ReadQuery_ListParts{ListParts: &pb.ListPartsQuery{
		BucketId:         uint64(info.ID),
		Key:              key,
		UploadId:         uploadID,
		PartNumberMarker: uint32(opts.PartNumberMarker),
		Limit:            uint32(maxParts),
	}}}
	res, err := s.router.Read(ctx, s.partitionOf(info.ID, key), q)
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	parts, err := decodeParts(res.GetParts().GetParts())
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	checksum, err := checksumAlgorithmOf(res.GetParts().GetChecksumAlgorithm())
	if err != nil {
		return nil, object.ListPartsResult{}, err
	}
	out := object.ListPartsResult{
		IsTruncated:       res.GetParts().GetMore(),
		ChecksumAlgorithm: checksum,
	}
	if n := len(parts); n > 0 {
		out.NextPartNumberMarker = parts[n-1].Number
	}
	return parts, out, nil
}

// CompleteUpload implements meta.Store. The object's parts are the stored
// parts that rec lists; the parts that are stored but not listed are returned
// as unused.
func (s *Store) CompleteUpload(ctx context.Context, uploadID string, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, []meta.PartRecord, error) {
	if err := object.ValidateKey(rec.Key); err != nil {
		return nil, nil, err
	}
	info, _, err := s.lookupBucket(ctx, rec.Bucket, false)
	if err != nil {
		return nil, nil, err
	}
	orec, err := recordpb.ObjectToProto(rec)
	if err != nil {
		return nil, nil, fmt.Errorf("raftstore: encode object: %w", err)
	}
	cmd := &pb.Command{Cmd: &pb.Command_CompleteUpload{CompleteUpload: &pb.CompleteUpload{
		BucketId:   uint64(info.ID),
		Key:        rec.Key,
		UploadId:   uploadID,
		Record:     orec,
		Conditions: conditionsToProto(cond),
	}}}
	res, err := s.router.Propose(ctx, s.partitionOf(info.ID, rec.Key), cmd)
	if err != nil {
		return nil, nil, err
	}
	garbage, err := decodeObjects(res.GetGarbage())
	if err != nil {
		return nil, nil, err
	}
	unused, err := decodeParts(res.GetUnusedParts())
	if err != nil {
		return nil, nil, err
	}
	return garbage, unused, nil
}

// AbortUpload implements meta.Store. It returns every part of the upload.
func (s *Store) AbortUpload(ctx context.Context, bucket, key, uploadID string) ([]meta.PartRecord, error) {
	info, _, err := s.lookupBucket(ctx, bucket, false)
	if err != nil {
		return nil, err
	}
	cmd := &pb.Command{Cmd: &pb.Command_AbortUpload{AbortUpload: &pb.AbortUpload{
		BucketId: uint64(info.ID),
		Key:      key,
		UploadId: uploadID,
	}}}
	res, err := s.router.Propose(ctx, s.partitionOf(info.ID, key), cmd)
	if err != nil {
		return nil, err
	}
	return decodeParts(res.GetUnusedParts())
}

// checksumAlgorithmOf converts a stored checksum algorithm to the domain type.
// A value outside the defined range is an error, never a truncated value.
func checksumAlgorithmOf(v pb.ChecksumAlgorithm) (object.ChecksumAlgorithm, error) {
	if v < 0 || v > pb.ChecksumAlgorithm_CHECKSUM_ALGORITHM_SHA256 {
		return 0, fmt.Errorf("raftstore: unknown checksum algorithm %d", v)
	}
	return object.ChecksumAlgorithm(v), nil
}
