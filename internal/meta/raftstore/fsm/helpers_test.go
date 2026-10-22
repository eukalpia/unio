package fsm

import (
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// testBucket is the bucket most data-partition tests write to.
const testBucket = ids.BucketID(1)

// otherBucket is a second bucket, for isolation checks.
const otherBucket = ids.BucketID(2)

// apply encodes cmd, applies it at index and returns the proposer's outcome.
func apply(t *testing.T, f *FSM, index uint64, cmd *pb.Command) *Outcome {
	t.Helper()
	data, err := cmd.MarshalVT()
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	res := f.Apply(&raft.Log{Index: index, Type: raft.LogCommand, Data: data})
	out, ok := res.(*Outcome)
	if !ok {
		t.Fatalf("Apply returned %T, want *Outcome", res)
	}
	return out
}

// harness drives one FSM with consecutive Raft indexes. It owns the database
// directory and reopens it on demand.
type harness struct {
	t     *testing.T
	kind  Kind
	dir   string
	cache *pebble.Cache
	f     *FSM
	index uint64
}

// newHarness opens a fresh FSM of the given kind in a temporary directory.
func newHarness(t *testing.T, kind Kind) *harness {
	t.Helper()
	cache := pebble.NewCache(16 << 20)
	t.Cleanup(cache.Unref)
	h := &harness{t: t, kind: kind, dir: t.TempDir(), cache: cache}
	h.open()
	// Registered last, so it runs first: the database closes before the
	// directory is removed and the cache is released.
	t.Cleanup(func() { _ = h.f.Close() })
	return h
}

// open opens the FSM directory.
func (h *harness) open() {
	h.t.Helper()
	f, err := Open(h.dir, h.cache, h.kind)
	if err != nil {
		h.t.Fatalf("open fsm: %v", err)
	}
	h.f = f
}

// reopen closes the database and opens the same directory again.
func (h *harness) reopen() {
	h.t.Helper()
	if err := h.f.Close(); err != nil {
		h.t.Fatalf("close fsm: %v", err)
	}
	h.open()
}

// do applies cmd at the next index and returns the outcome.
func (h *harness) do(cmd *pb.Command) *Outcome {
	h.t.Helper()
	h.index++
	return apply(h.t, h.f, h.index, cmd)
}

// mustApply applies cmd, requires success and returns its result.
func (h *harness) mustApply(cmd *pb.Command) *pb.ApplyResult {
	h.t.Helper()
	out := h.do(cmd)
	if out.Err != nil {
		h.t.Fatalf("apply at %d: unexpected error: %v", h.index, out.Err)
	}
	if out.Result == nil {
		return &pb.ApplyResult{}
	}
	return out.Result
}

// mustReject applies cmd, requires that it is rejected and returns the error.
func (h *harness) mustReject(cmd *pb.Command) error {
	h.t.Helper()
	out := h.do(cmd)
	if out.Err == nil {
		h.t.Fatalf("apply at %d: want an error, got result %v", h.index, out.Result)
	}
	return out.Err
}

// read runs a read query.
func (h *harness) read(q *pb.ReadQuery) (*pb.ReadResult, error) {
	h.t.Helper()
	return h.f.Read(q)
}

// getObject reads one version of key in testBucket.
func (h *harness) getObject(key, versionID string) (*pb.ObjectRecord, error) {
	h.t.Helper()
	res, err := h.read(getObjectQuery(testBucket, key, versionID))
	if err != nil {
		return nil, err
	}
	return res.GetObject().GetRecord(), nil
}

// dumpState returns every key and value of the database, except the applied
// index. Two equal dumps mean the two states are byte-for-byte identical.
func (h *harness) dumpState() map[string]string {
	h.t.Helper()
	return dumpDB(h.t, h.f)
}

// assertUnchanged fails when the state differs from before.
func (h *harness) assertUnchanged(before map[string]string) {
	h.t.Helper()
	after := h.dumpState()
	if len(after) != len(before) {
		h.t.Fatalf("state changed: %d keys before, %d after", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			h.t.Fatalf("state changed at key %q", k)
		}
	}
}

// dumpDB returns the database contents minus the applied index.
func dumpDB(t *testing.T, f *FSM) map[string]string {
	t.Helper()
	it, err := f.db.NewIter(nil)
	if err != nil {
		t.Fatalf("iterate: %v", err)
	}
	out := make(map[string]string)
	for ok := it.First(); ok; ok = it.Next() {
		out[string(it.Key())] = string(it.Value())
	}
	if err := closeIter(it, it.Error()); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	delete(out, string(keys.Meta(metaApplied)))
	return out
}

// wantCode fails unless err carries the S3 code.
func wantCode(t *testing.T, err error, code s3err.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %s, got nil", code)
	}
	if got := s3err.CodeOf(err); got != code {
		t.Fatalf("want error code %s, got %s (%v)", code, got, err)
	}
}

// objRec returns an object record with the given key, version id and etag.
func objRec(key, versionID, etag string) *pb.ObjectRecord {
	return &pb.ObjectRecord{
		Bucket:          "bkt",
		Key:             key,
		VersionId:       versionID,
		Etag:            etag,
		Size:            3,
		ModTimeUnixNano: 1,
	}
}

// putCmd builds a PutObject command.
func putCmd(bucket ids.BucketID, rec *pb.ObjectRecord, cond *pb.WriteConditions) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_PutObject{PutObject: &pb.PutObject{
		BucketId:   uint64(bucket),
		Record:     rec,
		Conditions: cond,
	}}}
}

// deleteCmd builds a DeleteObject command. markerID is used only when
// versionID is empty and the bucket is versioned or suspended.
func deleteCmd(bucket ids.BucketID, key, versionID, markerID string, cond *pb.WriteConditions) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_DeleteObject{DeleteObject: &pb.DeleteObject{
		BucketId:        uint64(bucket),
		Bucket:          "bkt",
		Key:             key,
		VersionId:       versionID,
		Conditions:      cond,
		ModTimeUnixNano: 2,
		MarkerVersionId: markerID,
	}}}
}

// versioningCmd builds a SetPartitionVersioning command.
func versioningCmd(bucket ids.BucketID, state pb.VersioningState) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_SetPartitionVersioning{SetPartitionVersioning: &pb.SetPartitionVersioning{
		BucketId:   uint64(bucket),
		Versioning: state,
	}}}
}

// fenceCmd builds a FenceBucket command.
func fenceCmd(bucket ids.BucketID, fenced bool) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_FenceBucket{FenceBucket: &pb.FenceBucket{
		BucketId: uint64(bucket),
		Fenced:   fenced,
	}}}
}

// getObjectQuery builds a GetObject read query.
func getObjectQuery(bucket ids.BucketID, key, versionID string) *pb.ReadQuery {
	return &pb.ReadQuery{Query: &pb.ReadQuery_GetObject{GetObject: &pb.GetObjectQuery{
		BucketId:  uint64(bucket),
		Bucket:    "bkt",
		Key:       key,
		VersionId: versionID,
	}}}
}

// versionsOf returns the stored versions of key in testBucket, newest first,
// by scanning the database. It is independent of the FSM's lookup helpers.
func (h *harness) versionsOf(key string) []*pb.ObjectRecord {
	h.t.Helper()
	prefix := keys.ObjectVersionPrefix(testBucket, []byte(key))
	it, err := h.f.prefixIter(prefix)
	if err != nil {
		h.t.Fatalf("iterate: %v", err)
	}
	var out []*pb.ObjectRecord
	for ok := it.First(); ok; ok = it.Next() {
		rec := &pb.ObjectRecord{}
		if err := rec.UnmarshalVT(it.Value()); err != nil {
			h.t.Fatalf("decode: %v", err)
		}
		out = append(out, rec)
	}
	if err := closeIter(it, it.Error()); err != nil {
		h.t.Fatalf("iterate: %v", err)
	}
	return out
}
