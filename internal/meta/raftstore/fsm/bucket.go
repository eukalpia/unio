package fsm

import (
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// errNoBucket is returned when a bucket does not exist, or is not the bucket
// (by id) the command names.
var errNoBucket = s3err.Errorf(s3err.NoSuchBucket, "bucket does not exist")

// fenceBucket sets or clears the fence of a bucket in a data partition.
func (f *FSM) fenceBucket(b *pebble.Batch, c *pb.FenceBucket) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	ctrl.fenced = c.Fenced
	if err := setControl(b, bucket, ctrl); err != nil {
		return nil, err
	}
	return &pb.ApplyResult{}, nil
}

// setPartitionVersioning copies a bucket's versioning state into a data
// partition.
func (f *FSM) setPartitionVersioning(b *pebble.Batch, c *pb.SetPartitionVersioning) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	ctrl.versioning = c.Versioning
	if err := setControl(b, bucket, ctrl); err != nil {
		return nil, err
	}
	return &pb.ApplyResult{}, nil
}

// bucketRecord reads a bucket record by name, including buckets marked
// deleting. It returns nil when the bucket does not exist.
func (f *FSM) bucketRecord(name string) (*pb.BucketRecord, error) {
	v, found, err := f.get(keys.Bucket([]byte(name)))
	if err != nil || !found {
		return nil, err
	}
	rec := &pb.BucketRecord{}
	if err := rec.UnmarshalVT(v); err != nil {
		return nil, fmt.Errorf("fsm: decode bucket: %w", err)
	}
	return rec, nil
}

// putBucket stores a bucket record in batch b.
func putBucket(b *pebble.Batch, rec *pb.BucketRecord) error {
	v, err := rec.MarshalVT()
	if err != nil {
		return fmt.Errorf("fsm: encode bucket: %w", err)
	}
	if err := b.Set(keys.Bucket([]byte(rec.Name)), v, nil); err != nil {
		return fmt.Errorf("fsm: write bucket: %w", err)
	}
	return nil
}

// createBucket adds a bucket and assigns its id from a counter that never
// reuses values, so a re-created bucket never resurrects old data.
func (f *FSM) createBucket(b *pebble.Batch, c *pb.CreateBucket) (*pb.ApplyResult, error) {
	if c.Name == "" {
		return nil, errMalformed("create bucket")
	}
	existing, err := f.bucketRecord(c.Name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Owner == c.Owner {
			return nil, s3err.Errorf(s3err.BucketAlreadyOwnedByYou, "bucket already exists and is owned by you")
		}
		return nil, s3err.Errorf(s3err.BucketAlreadyExists, "bucket already exists")
	}
	seq, err := f.readUint64(keys.Meta(metaBucketSequence))
	if err != nil {
		return nil, err
	}
	seq++
	if err := setUint64(b, keys.Meta(metaBucketSequence), seq); err != nil {
		return nil, err
	}
	rec := &pb.BucketRecord{
		Name:            c.Name,
		Id:              seq,
		CreatedUnixNano: c.CreatedUnixNano,
		Owner:           c.Owner,
		Versioning:      c.Versioning,
	}
	if err := putBucket(b, rec); err != nil {
		return nil, err
	}
	return &pb.ApplyResult{Bucket: rec}, nil
}

// currentBucket returns the bucket record for name and id, or errNoBucket.
func (f *FSM) currentBucket(name string, id uint64) (*pb.BucketRecord, error) {
	rec, err := f.bucketRecord(name)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.Id != id {
		return nil, errNoBucket
	}
	return rec, nil
}

// markBucketDeleting sets or clears the deleting flag of a bucket.
func (f *FSM) markBucketDeleting(b *pebble.Batch, c *pb.MarkBucketDeleting) (*pb.ApplyResult, error) {
	rec, err := f.currentBucket(c.Name, c.BucketId)
	if err != nil {
		return nil, err
	}
	rec.Deleting = c.Deleting
	if err := putBucket(b, rec); err != nil {
		return nil, err
	}
	return &pb.ApplyResult{Bucket: rec}, nil
}

// deleteBucket removes a bucket that is marked deleting. The caller has
// already verified that the bucket holds no data.
func (f *FSM) deleteBucket(b *pebble.Batch, c *pb.DeleteBucket) (*pb.ApplyResult, error) {
	rec, err := f.currentBucket(c.Name, c.BucketId)
	if err != nil {
		return nil, err
	}
	if !rec.Deleting {
		return nil, fmt.Errorf("fsm: bucket %q is not marked deleting", c.Name)
	}
	if err := b.Delete(keys.Bucket([]byte(c.Name)), nil); err != nil {
		return nil, fmt.Errorf("fsm: delete bucket: %w", err)
	}
	return &pb.ApplyResult{}, nil
}

// setBucketVersioning changes the versioning state of a bucket that is not
// being deleted.
func (f *FSM) setBucketVersioning(b *pebble.Batch, c *pb.SetBucketVersioning) (*pb.ApplyResult, error) {
	rec, err := f.currentBucket(c.Name, c.BucketId)
	if err != nil {
		return nil, err
	}
	if rec.Deleting {
		return nil, errNoBucket
	}
	rec.Versioning = c.Versioning
	if err := putBucket(b, rec); err != nil {
		return nil, err
	}
	return &pb.ApplyResult{Bucket: rec}, nil
}
