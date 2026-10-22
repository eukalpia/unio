package fsm

import (
	"bytes"
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	"github.com/eukalpia/unio/internal/object"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// errBucketGone is returned by data commands for a fenced bucket: the bucket
// is being deleted and accepts no new data.
var errBucketGone = s3err.Errorf(s3err.NoSuchBucket, "bucket does not exist")

// bucketControl is the per-bucket state a data partition keeps in its log.
type bucketControl struct {
	fenced     bool
	versioning pb.VersioningState
}

// control reads the control record of a bucket. A missing record means
// unversioned and not fenced.
func (f *FSM) control(bucket ids.BucketID) (bucketControl, error) {
	v, found, err := f.get(keys.Control(bucket))
	if err != nil || !found {
		return bucketControl{}, err
	}
	if len(v) != 2 {
		return bucketControl{}, fmt.Errorf("fsm: control record of bucket %d has %d bytes", bucket, len(v))
	}
	return bucketControl{fenced: v[0] == 1, versioning: pb.VersioningState(v[1])}, nil
}

// setControl stores the control record of a bucket in batch b.
func setControl(b *pebble.Batch, bucket ids.BucketID, c bucketControl) error {
	var fenced byte
	if c.fenced {
		fenced = 1
	}
	if err := b.Set(keys.Control(bucket), []byte{fenced, byte(c.versioning)}, nil); err != nil {
		return fmt.Errorf("fsm: write control: %w", err)
	}
	return nil
}

// versioned reports whether writes create new versions with ids.
func (c bucketControl) versioned() bool {
	return c.versioning == pb.VersioningState_VERSIONING_STATE_ENABLED
}

// checkConditions evaluates the write preconditions against the latest
// version of a key, exactly as object.Conditions.EvaluateWrite defines.
func checkConditions(c *pb.WriteConditions, latest *pb.ObjectRecord) error {
	exists := latest != nil && !latest.DeleteMarker
	etag := ""
	if exists {
		etag = latest.Etag
	}
	cond := object.Conditions{IfMatch: c.GetIfMatch(), IfNoneMatch: c.GetIfNoneMatch()}
	return cond.EvaluateWrite(exists, etag)
}

// addVersion stores a new version of key in batch b and returns the garbage
// it replaces. In a versioned bucket rec must carry a new, non-empty version
// id and the version is added. Otherwise rec must carry the empty id and it
// replaces the null version. The second result is the version id to report.
func (f *FSM) addVersion(b *pebble.Batch, ctrl bucketControl, bucket ids.BucketID, key []byte, rec *pb.ObjectRecord) ([]*pb.ObjectRecord, error) {
	if ctrl.versioned() {
		if rec.VersionId == "" {
			return nil, errInvalid("version id required in a versioned bucket")
		}
		dup, _, err := f.findVersion(bucket, key, rec.VersionId)
		if err != nil {
			return nil, err
		}
		if dup != nil {
			return nil, errInvalid("version id already in use")
		}
		ts, err := f.nextTimestamp(b)
		if err != nil {
			return nil, err
		}
		return nil, writeVersion(b, bucket, key, ts, rec)
	}
	if rec.VersionId != "" {
		return nil, errInvalid("version id given in an unversioned bucket")
	}
	var garbage []*pb.ObjectRecord
	old, oldKey, err := f.findVersion(bucket, key, "")
	if err != nil {
		return nil, err
	}
	if old != nil {
		if err := b.Delete(oldKey, nil); err != nil {
			return nil, fmt.Errorf("fsm: delete replaced version: %w", err)
		}
		garbage = append(garbage, old)
	}
	ts, err := f.nextTimestamp(b)
	if err != nil {
		return nil, err
	}
	return garbage, writeVersion(b, bucket, key, ts, rec)
}

// putObject commits a new version of a key.
func (f *FSM) putObject(b *pebble.Batch, c *pb.PutObject) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	rec := c.GetRecord()
	if rec == nil || rec.Key == "" {
		return nil, errMalformed("put object")
	}
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	if ctrl.fenced {
		return nil, errBucketGone
	}
	key := []byte(rec.Key)
	latest, _, err := f.latestVersion(bucket, key)
	if err != nil {
		return nil, err
	}
	if err := checkConditions(c.GetConditions(), latest); err != nil {
		return nil, err
	}
	garbage, err := f.addVersion(b, ctrl, bucket, key, rec)
	if err != nil {
		return nil, err
	}
	return &pb.ApplyResult{VersionId: rec.VersionId, Garbage: garbage}, nil
}

// deleteObject removes one version, or adds a delete marker when no version
// id is given. Deleting a missing version, or a missing null version, is a
// success that changes nothing.
func (f *FSM) deleteObject(b *pebble.Batch, c *pb.DeleteObject) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	key := []byte(c.Key)
	ctrl, err := f.control(bucket)
	if err != nil {
		return nil, err
	}
	latest, _, err := f.latestVersion(bucket, key)
	if err != nil {
		return nil, err
	}
	if err := checkConditions(c.GetConditions(), latest); err != nil {
		return nil, err
	}
	res := &pb.ApplyResult{}
	if c.VersionId != "" {
		rec, dbKey, err := f.findVersion(bucket, key, c.VersionId)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			return res, nil
		}
		if err := b.Delete(dbKey, nil); err != nil {
			return nil, fmt.Errorf("fsm: delete version: %w", err)
		}
		res.Garbage = append(res.Garbage, rec)
		if rec.DeleteMarker {
			res.DeleteMarker = true
			res.DeleteMarkerVersionId = rec.VersionId
		}
		return res, nil
	}
	if ctrl.versioned() {
		marker := &pb.ObjectRecord{
			Bucket:          c.Bucket,
			Key:             c.Key,
			VersionId:       c.MarkerVersionId,
			ModTimeUnixNano: c.ModTimeUnixNano,
			DeleteMarker:    true,
		}
		if _, err := f.addVersion(b, ctrl, bucket, key, marker); err != nil {
			return nil, err
		}
		res.DeleteMarker = true
		res.DeleteMarkerVersionId = c.MarkerVersionId
		return res, nil
	}
	old, oldKey, err := f.findVersion(bucket, key, "")
	if err != nil {
		return nil, err
	}
	if old != nil {
		if err := b.Delete(oldKey, nil); err != nil {
			return nil, fmt.Errorf("fsm: delete null version: %w", err)
		}
		res.Garbage = append(res.Garbage, old)
	}
	if ctrl.versioning == pb.VersioningState_VERSIONING_STATE_SUSPENDED {
		// A suspended bucket records the delete as a delete marker with the
		// null id, replacing the null version.
		marker := &pb.ObjectRecord{Bucket: c.Bucket, Key: c.Key, ModTimeUnixNano: c.ModTimeUnixNano, DeleteMarker: true}
		ts, err := f.nextTimestamp(b)
		if err != nil {
			return nil, err
		}
		if err := writeVersion(b, bucket, key, ts, marker); err != nil {
			return nil, err
		}
		res.DeleteMarker = true
	}
	return res, nil
}

// updateLayout replaces the layout of one part of a version that still
// references the expected data. Otherwise it fails with
// meta.ErrRevisionMismatch.
func (f *FSM) updateLayout(b *pebble.Batch, c *pb.UpdateLayout) (*pb.ApplyResult, error) {
	bucket := ids.BucketID(c.BucketId)
	rec, dbKey, err := f.resolveVersion(bucket, []byte(c.Key), c.VersionId)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.DeleteMarker {
		return nil, meta.ErrRevisionMismatch
	}
	idx := -1
	for i, p := range rec.Parts {
		if p.Number == c.PartNumber {
			idx = i
			break
		}
	}
	if idx < 0 || !bytes.Equal(rec.Parts[idx].Data, c.Data) {
		return nil, meta.ErrRevisionMismatch
	}
	rec.Parts[idx].Layout = c.Layout
	v, err := rec.MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("fsm: encode object: %w", err)
	}
	if err := b.Set(dbKey, v, nil); err != nil {
		return nil, fmt.Errorf("fsm: write layout: %w", err)
	}
	return &pb.ApplyResult{}, nil
}

// errMalformed reports a command that the proposer should never send.
func errMalformed(what string) error {
	return fmt.Errorf("fsm: malformed %s command", what)
}

// errInvalid reports a client error in a command: the request, not the state
// machine, is at fault.
func errInvalid(msg string) error {
	return s3err.Errorf(s3err.InvalidArgument, "%s", msg)
}
