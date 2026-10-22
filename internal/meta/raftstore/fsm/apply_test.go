package fsm

import (
	"errors"
	"testing"

	"github.com/hashicorp/raft"

	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

func TestApplyUnknownCommand(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))

	before := h.dumpState()
	if err := h.mustReject(&pb.Command{}); err == nil {
		t.Fatal("empty command: want error")
	}
	h.assertUnchanged(before)
	if h.f.Applied() != h.index {
		t.Fatalf("Applied = %d after a rejected command, want %d", h.f.Applied(), h.index)
	}
}

// Commands for the other group are routing bugs and must fail without
// touching the state.
func TestWrongGroupCommands(t *testing.T) {
	t.Run("system command on a data partition", func(t *testing.T) {
		h := newHarness(t, KindData)
		before := h.dumpState()
		h.mustReject(createBucketCmd("a", "alice"))
		h.mustReject(kvPutCmd("k", []byte("v"), 0))
		h.assertUnchanged(before)
	})

	t.Run("data command on the system group", func(t *testing.T) {
		h := newHarness(t, KindSystem)
		before := h.dumpState()
		h.mustReject(putCmd(testBucket, objRec("k", "", etagA), nil))
		h.mustReject(createUploadCmd(testBucket, "k", "u1"))
		h.mustReject(fenceCmd(testBucket, true))
		h.assertUnchanged(before)
	})

	t.Run("read queries across groups", func(t *testing.T) {
		data := newHarness(t, KindData)
		if _, err := data.read(&pb.ReadQuery{Query: &pb.ReadQuery_KvGet{KvGet: &pb.KVGetQuery{Key: "k"}}}); err == nil {
			t.Fatal("KV read on a data partition: want error")
		}
		system := newHarness(t, KindSystem)
		if _, err := system.read(getObjectQuery(testBucket, "k", "")); err == nil {
			t.Fatal("object read on the system group: want error")
		}
	})
}

// A log entry at or below the applied index is already reflected in the state
// and must not change it.
func TestApplyReplaySkipsAppliedIndexes(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))
	h.mustApply(putCmd(testBucket, objRec("j", "", etagA), nil))
	h.mustApply(putCmd(testBucket, objRec("m", "", etagA), nil))
	before := h.dumpState()

	// Replay index 2 with a different command: the replay must be ignored.
	out := apply(t, h.f, 2, deleteCmd(testBucket, "k", "", "", nil))
	if out.Err != nil || out.Result != nil {
		t.Fatalf("replayed index: outcome %+v, want empty", out)
	}
	// Replaying the newest index is also a no-op.
	out = apply(t, h.f, h.index, putCmd(testBucket, objRec("z", "", etagB), nil))
	if out.Err != nil || out.Result != nil {
		t.Fatalf("replayed newest index: outcome %+v, want empty", out)
	}
	h.assertUnchanged(before)
	if h.f.Applied() != h.index {
		t.Fatalf("Applied = %d, want %d", h.f.Applied(), h.index)
	}
}

// Every applied command persists its own index, including a rejected one.
func TestRejectedCommandAdvancesAppliedIndex(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustReject(putCmd(testBucket, objRec("k", "v1", etagA), nil)) // not versioned
	if got := h.f.Applied(); got != 1 {
		t.Fatalf("Applied = %d after one rejected command, want 1", got)
	}
}

func TestApplyDecodeError(t *testing.T) {
	h := newHarness(t, KindData)
	// Field 1 with wire type 7 is not a valid protobuf encoding.
	out := h.f.Apply(&raft.Log{Index: 1, Type: raft.LogCommand, Data: []byte{0x0f}}).(*Outcome)
	if out.Err == nil {
		t.Fatal("undecodable entry: want error")
	}
	if h.f.Applied() != 1 {
		t.Fatalf("Applied = %d, want 1", h.f.Applied())
	}
}

// Reopening the directory keeps the applied index and the state, and replaying
// the same log entries after a restart changes nothing.
func TestReopenKeepsAppliedIndexAndState(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(versioningCmd(testBucket, enabled))
	h.mustApply(putCmd(testBucket, objRec("k", "v1", etagA), nil))
	h.mustApply(createUploadCmd(testBucket, "k", "u1"))
	h.mustApply(putPartCmd(testBucket, "k", "u1", 1, "p1", partData(1)))
	h.mustReject(putCmd(testBucket, objRec("k", "", etagB), nil))

	applied := h.f.Applied()
	state := h.dumpState()

	h.reopen()
	if h.f.Applied() != applied {
		t.Fatalf("Applied after reopen = %d, want %d", h.f.Applied(), applied)
	}
	if got := h.dumpState(); !equalState(got, state) {
		t.Fatal("state after reopen differs")
	}
	if _, err := h.getObject("k", "v1"); err != nil {
		t.Fatalf("v1 after reopen: %v", err)
	}

	// Replay the whole log after the restart: every entry is at or below the
	// applied index, so nothing changes.
	apply(t, h.f, 3, putCmd(testBucket, objRec("k", "v9", etagB), nil))
	if got := h.dumpState(); !equalState(got, state) {
		t.Fatal("replay after reopen changed the state")
	}
}

// After a storage failure every command and read fails with ErrFailed. The
// failure is injected here because a clean real trigger is not available; the
// test covers the latch that all entry points share.
func TestStorageFailureIsFatal(t *testing.T) {
	h := newHarness(t, KindData)
	h.mustApply(putCmd(testBucket, objRec("k", "", etagA), nil))

	h.f.fail(errors.New("injected disk failure"))

	out := h.do(putCmd(testBucket, objRec("j", "", etagA), nil))
	if !errors.Is(out.Err, ErrFailed) {
		t.Fatalf("apply after failure: %v, want ErrFailed", out.Err)
	}
	if _, err := h.read(getObjectQuery(testBucket, "k", "")); !errors.Is(err, ErrFailed) {
		t.Fatalf("read after failure: %v, want ErrFailed", err)
	}
	if _, err := h.f.Snapshot(); !errors.Is(err, ErrFailed) {
		t.Fatalf("snapshot after failure: %v, want ErrFailed", err)
	}
	out = h.do(deleteCmd(testBucket, "k", "", "", nil))
	if !errors.Is(out.Err, ErrFailed) {
		t.Fatalf("second apply after failure: %v, want ErrFailed", out.Err)
	}
}

// equalState reports whether two dumps hold the same keys and values.
func equalState(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
