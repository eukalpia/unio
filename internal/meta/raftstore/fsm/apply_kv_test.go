package fsm

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func kvPutCmd(key string, value []byte, expect uint64) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_KvPut{KvPut: &pb.KVPut{
		Key:            key,
		Value:          value,
		ExpectRevision: expect,
	}}}
}

func kvDeleteCmd(key string, expect uint64) *pb.Command {
	return &pb.Command{Cmd: &pb.Command_KvDelete{KvDelete: &pb.KVDelete{
		Key:            key,
		ExpectRevision: expect,
	}}}
}

// kvGet reads one entry.
func (h *harness) kvGet(key string) (*pb.KVEntry, error) {
	h.t.Helper()
	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_KvGet{KvGet: &pb.KVGetQuery{Key: key}}})
	if err != nil {
		return nil, err
	}
	return res.GetKv().GetEntries()[0], nil
}

func TestKVPutRevisions(t *testing.T) {
	h := newHarness(t, KindSystem)

	// Absent keys have revision 0, and the stored revision is the log index.
	res := h.mustApply(kvPutCmd("k", []byte("v1"), 0))
	first := h.index
	if res.Revision != first {
		t.Fatalf("revision = %d, want the log index %d", res.Revision, first)
	}

	before := h.dumpState()
	if err := h.mustReject(kvPutCmd("k", []byte("x"), 0)); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("create over an existing key: %v, want ErrRevisionMismatch", err)
	}
	h.assertUnchanged(before)

	if err := h.mustReject(kvPutCmd("k", []byte("x"), first+1)); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("stale revision: %v, want ErrRevisionMismatch", err)
	}

	res = h.mustApply(kvPutCmd("k", []byte("v2"), first))
	second := h.index
	if res.Revision != second {
		t.Fatalf("revision after update = %d, want %d", res.Revision, second)
	}

	res = h.mustApply(kvPutCmd("k", []byte("v3"), meta.AnyRevision))
	entry, err := h.kvGet("k")
	if err != nil || string(entry.Value) != "v3" || entry.Revision != h.index || res.Revision != h.index {
		t.Fatalf("AnyRevision write: entry %v, %v; want v3 at revision %d", entry, err, h.index)
	}
}

func TestKVPutValueSizeLimit(t *testing.T) {
	h := newHarness(t, KindSystem)

	max := bytes.Repeat([]byte{'x'}, meta.MaxSystemValueSize)
	h.mustApply(kvPutCmd("big", max, 0))
	entry, err := h.kvGet("big")
	if err != nil || len(entry.Value) != meta.MaxSystemValueSize {
		t.Fatalf("max-size value: len %d, %v", len(entry.Value), err)
	}

	before := h.dumpState()
	if err := h.mustReject(kvPutCmd("huge", make([]byte, meta.MaxSystemValueSize+1), 0)); !errors.Is(err, meta.ErrValueTooLarge) {
		t.Fatalf("oversized value: %v, want ErrValueTooLarge", err)
	}
	h.assertUnchanged(before)
}

func TestKVEmptyValueIsPresent(t *testing.T) {
	h := newHarness(t, KindSystem)
	h.mustApply(kvPutCmd("empty", nil, 0))

	entry, err := h.kvGet("empty")
	if err != nil || len(entry.Value) != 0 || entry.Revision == 0 {
		t.Fatalf("empty value: %v, %v; want present with revision", entry, err)
	}
	// An empty value still has a revision, so creating again must fail.
	h.mustReject(kvPutCmd("empty", []byte("x"), 0))
}

func TestKVDeleteRevisions(t *testing.T) {
	h := newHarness(t, KindSystem)

	// Deleting an absent key succeeds for expect 0 and AnyRevision only.
	h.mustApply(kvDeleteCmd("absent", 0))
	h.mustApply(kvDeleteCmd("absent", meta.AnyRevision))
	if err := h.mustReject(kvDeleteCmd("absent", 5)); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("delete absent with expect 5: %v, want ErrRevisionMismatch", err)
	}

	h.mustApply(kvPutCmd("k", []byte("v"), 0))
	rev := h.index
	before := h.dumpState()
	if err := h.mustReject(kvDeleteCmd("k", rev+1)); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("delete with stale revision: %v, want ErrRevisionMismatch", err)
	}
	if err := h.mustReject(kvDeleteCmd("k", 0)); !errors.Is(err, meta.ErrRevisionMismatch) {
		t.Fatalf("delete present with expect 0: %v, want ErrRevisionMismatch", err)
	}
	h.assertUnchanged(before)

	h.mustApply(kvDeleteCmd("k", rev))
	if _, err := h.kvGet("k"); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("get after delete: %v, want ErrNotFound", err)
	}

	h.mustApply(kvPutCmd("k", []byte("again"), 0))
	h.mustApply(kvDeleteCmd("k", meta.AnyRevision))
	if _, err := h.kvGet("k"); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("AnyRevision delete left the key: %v", err)
	}
}

func TestKVGetMissing(t *testing.T) {
	h := newHarness(t, KindSystem)
	if _, err := h.kvGet("nope"); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("missing key: %v, want ErrNotFound", err)
	}
}

func TestKVListPrefix(t *testing.T) {
	h := newHarness(t, KindSystem)
	h.mustApply(kvPutCmd("app/b", []byte("2"), 0))
	h.mustApply(kvPutCmd("app/a", []byte("1"), 0))
	h.mustApply(kvPutCmd("apq", []byte("3"), 0))
	h.mustApply(kvPutCmd("other", []byte("4"), 0))

	res, err := h.read(&pb.ReadQuery{Query: &pb.ReadQuery_KvList{KvList: &pb.KVListQuery{Prefix: "app/"}}})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	entries := res.GetKv().GetEntries()
	if len(entries) != 2 || entries[0].Key != "app/a" || entries[1].Key != "app/b" {
		t.Fatalf("list app/ = %v, want app/a then app/b", entries)
	}
	if string(entries[0].Value) != "1" || entries[0].Revision == 0 {
		t.Fatalf("entry app/a = %v", entries[0])
	}

	res, err = h.read(&pb.ReadQuery{Query: &pb.ReadQuery_KvList{KvList: &pb.KVListQuery{Prefix: ""}}})
	if err != nil || len(res.GetKv().GetEntries()) != 4 {
		t.Fatalf("list all = %v, %v; want 4 entries", res, err)
	}
}
