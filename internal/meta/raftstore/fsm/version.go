package fsm

import (
	"bytes"
	"fmt"

	"github.com/cockroachdb/pebble/v2"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Every version of a key is stored under a commit counter that the state
// machine increments for each version it creates. Versions of a key sort in
// commit order, so the first version under the key prefix is the latest,
// regardless of clocks. A version carries an opaque VersionId chosen by the
// proposer (versioned buckets) or empty for the null version (unversioned and
// suspended buckets). Lookups by id scan the versions of one key, which are
// few in practice.

// nextTimestamp increments and returns the partition's commit counter, and
// writes it into batch b.
func (f *FSM) nextTimestamp(b *pebble.Batch) (uint64, error) {
	last, err := f.readUint64(keys.Meta(metaLastVersionTS))
	if err != nil {
		return 0, err
	}
	ts := last + 1
	if err := setUint64(b, keys.Meta(metaLastVersionTS), ts); err != nil {
		return 0, err
	}
	return ts, nil
}

// latestVersion returns the newest version of key (which may be a delete
// marker) and its database key, or nil when the key has no versions.
func (f *FSM) latestVersion(bucket ids.BucketID, key []byte) (*pb.ObjectRecord, []byte, error) {
	it, err := f.prefixIter(keys.ObjectVersionPrefix(bucket, key))
	if err != nil {
		return nil, nil, err
	}
	var (
		rec *pb.ObjectRecord
		k   []byte
	)
	if it.First() {
		k = bytes.Clone(it.Key())
		rec, err = unmarshalObject(it.Value())
	}
	return rec, k, closeIter(it, err)
}

// findVersion returns the version of key with the given id and its database
// key, or nil when no such version exists. The empty id finds the null
// version.
func (f *FSM) findVersion(bucket ids.BucketID, key []byte, versionID string) (*pb.ObjectRecord, []byte, error) {
	it, err := f.prefixIter(keys.ObjectVersionPrefix(bucket, key))
	if err != nil {
		return nil, nil, err
	}
	var (
		rec *pb.ObjectRecord
		k   []byte
	)
	for ok := it.First(); ok; ok = it.Next() {
		r, err := unmarshalObject(it.Value())
		if err != nil {
			return nil, nil, closeIter(it, err)
		}
		if r.VersionId == versionID {
			rec = r
			k = bytes.Clone(it.Key())
			break
		}
	}
	return rec, k, closeIter(it, it.Error())
}

// resolveVersion returns one version and its database key: the latest when
// versionID is empty, else the version with that id. The result is nil when
// the version does not exist.
func (f *FSM) resolveVersion(bucket ids.BucketID, key []byte, versionID string) (*pb.ObjectRecord, []byte, error) {
	if versionID == "" {
		return f.latestVersion(bucket, key)
	}
	return f.findVersion(bucket, key, versionID)
}

// writeVersion stores rec as the version with commit timestamp ts in batch b.
func writeVersion(b *pebble.Batch, bucket ids.BucketID, key []byte, ts uint64, rec *pb.ObjectRecord) error {
	v, err := rec.MarshalVT()
	if err != nil {
		return fmt.Errorf("fsm: encode object: %w", err)
	}
	if err := b.Set(keys.ObjectVersion(bucket, key, ts), v, nil); err != nil {
		return fmt.Errorf("fsm: write object: %w", err)
	}
	return nil
}

// unmarshalObject decodes a stored object record.
func unmarshalObject(v []byte) (*pb.ObjectRecord, error) {
	rec := &pb.ObjectRecord{}
	if err := rec.UnmarshalVT(v); err != nil {
		return nil, fmt.Errorf("fsm: decode object: %w", err)
	}
	return rec, nil
}
