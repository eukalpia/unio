package raftstore

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/recordpb"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxFanout bounds the concurrent requests of an operation that touches
// every data partition.
const maxFanout = 16

// CreateBucket implements meta.Store. The bucket is created in the system
// group, and a non-default versioning state is copied to every data
// partition before the call returns.
func (s *Store) CreateBucket(ctx context.Context, name string, opts object.MakeBucketOptions) (object.BucketInfo, error) {
	if err := object.ValidateBucketName(name); err != nil {
		return object.BucketInfo{}, err
	}
	cmd := &pb.Command{Cmd: &pb.Command_CreateBucket{CreateBucket: &pb.CreateBucket{
		Name:            name,
		Owner:           opts.Owner,
		Versioning:      pb.VersioningState(opts.Versioning),
		CreatedUnixNano: time.Now().UnixNano(),
	}}}
	res, err := s.router.Propose(ctx, ids.SystemPartition, cmd)
	if err != nil {
		return object.BucketInfo{}, err
	}
	info, _, err := recordpb.BucketFromProto(res.GetBucket())
	if err != nil {
		return object.BucketInfo{}, fmt.Errorf("raftstore: decode bucket: %w", err)
	}
	if opts.Versioning != object.VersioningUnversioned {
		if err := s.setPartitionVersioning(ctx, info.ID, opts.Versioning); err != nil {
			return object.BucketInfo{}, err
		}
	}
	return info, nil
}

// GetBucket implements meta.Store.
func (s *Store) GetBucket(ctx context.Context, name string) (object.BucketInfo, error) {
	info, _, err := s.lookupBucket(ctx, name, false)
	return info, err
}

// lookupBucket reads a bucket linearizably. Buckets being deleted are
// returned only when includeDeleting is set; the boolean reports the flag.
func (s *Store) lookupBucket(ctx context.Context, name string, includeDeleting bool) (object.BucketInfo, bool, error) {
	q := &pb.ReadQuery{Query: &pb.ReadQuery_GetBucket{GetBucket: &pb.GetBucketQuery{Name: name, IncludeDeleting: includeDeleting}}}
	res, err := s.router.Read(ctx, ids.SystemPartition, q)
	if err != nil {
		return object.BucketInfo{}, false, err
	}
	rec := res.GetBucket().GetBucket()
	if rec == nil {
		return object.BucketInfo{}, false, fmt.Errorf("raftstore: bucket read returned no bucket")
	}
	info, deleting, err := recordpb.BucketFromProto(rec)
	if err != nil {
		return object.BucketInfo{}, false, fmt.Errorf("raftstore: decode bucket: %w", err)
	}
	return info, deleting, nil
}

// ListBuckets implements meta.Store. Buckets are returned in name order.
func (s *Store) ListBuckets(ctx context.Context) ([]object.BucketInfo, error) {
	q := &pb.ReadQuery{Query: &pb.ReadQuery_ListBuckets{ListBuckets: &pb.ListBucketsQuery{}}}
	res, err := s.router.Read(ctx, ids.SystemPartition, q)
	if err != nil {
		return nil, err
	}
	out := make([]object.BucketInfo, 0, len(res.GetBuckets().GetBuckets()))
	for _, rec := range res.GetBuckets().GetBuckets() {
		info, _, err := recordpb.BucketFromProto(rec)
		if err != nil {
			return nil, fmt.Errorf("raftstore: decode bucket: %w", err)
		}
		out = append(out, info)
	}
	return out, nil
}

// DeleteBucket implements meta.Store. The protocol fences the bucket in every
// data partition before it counts the bucket's data, so no write can add
// data after the count. A non-empty bucket is unfenced again and the call
// fails with BucketNotEmpty. A bucket left in the deleting state by a crash is
// completed or reverted by the next DeleteBucket call.
func (s *Store) DeleteBucket(ctx context.Context, name string) error {
	info, deleting, err := s.lookupBucket(ctx, name, true)
	if err != nil {
		return err
	}
	if !deleting {
		cmd := &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{
			Name: name, BucketId: uint64(info.ID), Deleting: true,
		}}}
		if _, err := s.router.Propose(ctx, ids.SystemPartition, cmd); err != nil {
			return err
		}
	}
	if err := s.setFence(ctx, info.ID, true); err != nil {
		return errors.Join(err, s.revertDelete(ctx, info))
	}
	nonEmpty, err := s.anyBucketData(ctx, info.ID)
	if err != nil {
		return errors.Join(err, s.revertDelete(ctx, info))
	}
	if nonEmpty {
		if err := s.revertDelete(ctx, info); err != nil {
			return err
		}
		return s3err.Errorf(s3err.BucketNotEmpty, "the bucket you tried to delete is not empty")
	}
	cmd := &pb.Command{Cmd: &pb.Command_DeleteBucket{DeleteBucket: &pb.DeleteBucket{
		Name: name, BucketId: uint64(info.ID),
	}}}
	_, err = s.router.Propose(ctx, ids.SystemPartition, cmd)
	return err
}

// revertDelete lifts the fences and clears the deleting flag of a bucket. It
// uses a context that survives the caller's cancellation, because the bucket
// must not stay fenced. The flag is cleared only after every fence is lifted.
func (s *Store) revertDelete(ctx context.Context, info object.BucketInfo) error {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*s.opts.ApplyTimeout)
	defer cancel()
	if err := s.setFence(cctx, info.ID, false); err != nil {
		return fmt.Errorf("raftstore: lift fences of bucket %q: %w", info.Name, err)
	}
	cmd := &pb.Command{Cmd: &pb.Command_MarkBucketDeleting{MarkBucketDeleting: &pb.MarkBucketDeleting{
		Name: info.Name, BucketId: uint64(info.ID), Deleting: false,
	}}}
	if _, err := s.router.Propose(cctx, ids.SystemPartition, cmd); err != nil {
		return fmt.Errorf("raftstore: clear deleting flag of bucket %q: %w", info.Name, err)
	}
	return nil
}

// setFence fences or unfences a bucket in every data partition.
func (s *Store) setFence(ctx context.Context, bucket ids.BucketID, fenced bool) error {
	return s.eachDataPartition(ctx, func(ctx context.Context, p ids.PartitionID) error {
		cmd := &pb.Command{Cmd: &pb.Command_FenceBucket{FenceBucket: &pb.FenceBucket{
			BucketId: uint64(bucket), Fenced: fenced,
		}}}
		_, err := s.router.Propose(ctx, p, cmd)
		return err
	})
}

// anyBucketData reports whether any data partition holds data of a bucket.
func (s *Store) anyBucketData(ctx context.Context, bucket ids.BucketID) (bool, error) {
	var found atomic.Bool
	err := s.eachDataPartition(ctx, func(ctx context.Context, p ids.PartitionID) error {
		q := &pb.ReadQuery{Query: &pb.ReadQuery_BucketHasData{BucketHasData: &pb.BucketHasDataQuery{BucketId: uint64(bucket)}}}
		res, err := s.router.Read(ctx, p, q)
		if err != nil {
			return err
		}
		if res.GetBucketHasData().GetHasData() {
			found.Store(true)
		}
		return nil
	})
	return found.Load(), err
}

// SetBucketVersioning implements meta.Store. The state is written to the
// system group first and then copied to every data partition, so writes see
// it in their own log. A failure in the copy is reported; calling the
// operation again completes it.
func (s *Store) SetBucketVersioning(ctx context.Context, name string, state object.VersioningState) error {
	info, _, err := s.lookupBucket(ctx, name, false)
	if err != nil {
		return err
	}
	cmd := &pb.Command{Cmd: &pb.Command_SetBucketVersioning{SetBucketVersioning: &pb.SetBucketVersioning{
		Name: name, BucketId: uint64(info.ID), Versioning: pb.VersioningState(state),
	}}}
	if _, err := s.router.Propose(ctx, ids.SystemPartition, cmd); err != nil {
		return err
	}
	return s.setPartitionVersioning(ctx, info.ID, state)
}

// setPartitionVersioning copies a bucket's versioning state to every data
// partition.
func (s *Store) setPartitionVersioning(ctx context.Context, bucket ids.BucketID, state object.VersioningState) error {
	return s.eachDataPartition(ctx, func(ctx context.Context, p ids.PartitionID) error {
		cmd := &pb.Command{Cmd: &pb.Command_SetPartitionVersioning{SetPartitionVersioning: &pb.SetPartitionVersioning{
			BucketId: uint64(bucket), Versioning: pb.VersioningState(state),
		}}}
		_, err := s.router.Propose(ctx, p, cmd)
		return err
	})
}

// eachDataPartition runs fn for every data partition, with bounded
// concurrency. The first error cancels the others and is returned.
func (s *Store) eachDataPartition(ctx context.Context, fn func(ctx context.Context, p ids.PartitionID) error) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxFanout)
	for _, p := range s.dataParts {
		g.Go(func() error { return fn(gctx, p) })
	}
	return g.Wait()
}
