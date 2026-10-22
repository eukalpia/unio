package fsm

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/meta/raftstore/keys"
	"github.com/eukalpia/unio/internal/meta/raftstore/pebbleopt"
	pb "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// Kind selects the command family a state machine accepts.
type Kind uint8

// State machine kinds.
const (
	// KindData is a data partition: objects, uploads, parts, bucket control.
	KindData Kind = iota + 1
	// KindSystem is the system group: buckets and key-value entries.
	KindSystem
)

// ErrFailed is returned by every command after a storage failure. The state
// machine cannot know whether the failed batch reached disk, so it refuses
// further work; the node must restart and replay the log.
var ErrFailed = errors.New("fsm: state machine failed; restart the node")

// errPanicked is the outcome of an entry or read that panicked and was recovered.
var errPanicked = errors.New("fsm: command rejected after an internal error")

// ErrClosed is returned by reads that race with Close.
var ErrClosed = errors.New("fsm: state machine is closed")

// Outcome is the value Apply returns to the proposer: the result of a
// successful command, or the error that rejected it. Application errors
// leave the state unchanged.
type Outcome struct {
	Result *pb.ApplyResult
	Err    error
}

// FSM is a Raft state machine backed by one Pebble database. It implements
// raft.FSM. Apply, Snapshot and Restore are called by the Raft goroutine
// only; Read may be called concurrently with them.
type FSM struct {
	kind Kind
	db   *pebble.DB

	// applied is the index of the last command reflected in the database.
	// Only the Raft goroutine writes it; readers on other goroutines use
	// Applied to decide whether a read can be served from the state.
	applied atomic.Uint64

	// applyHook, when set, runs at the start of every Apply. Tests use it to
	// hold the state machine behind the log; see SetApplyHookForTesting.
	applyHook atomic.Pointer[func(index uint64)]

	mu    sync.Mutex
	fatal error

	// readMu orders reads against Close: a read holds the read lock while it
	// uses the database, and Close takes the write lock before closing it.
	readMu sync.RWMutex
	closed bool

	// panics counts recovered panics; see PanicCount.
	panics atomic.Int64
}

// Open opens or creates the state machine database in dir. cache is shared
// by all databases of a node and is owned by the caller.
func Open(dir string, cache *pebble.Cache, kind Kind) (*FSM, error) {
	if kind != KindData && kind != KindSystem {
		return nil, fmt.Errorf("fsm: unknown kind %d", kind)
	}
	db, err := pebble.Open(dir, pebbleopt.Options(cache))
	if err != nil {
		return nil, fmt.Errorf("fsm: open %s: %w", dir, err)
	}
	f := &FSM{kind: kind, db: db}
	applied, err := f.readUint64(keys.Meta(metaApplied))
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("fsm: read applied index: %w", err)
	}
	f.applied.Store(applied)
	return f, nil
}

// Close closes the database. The FSM must not be used afterwards.
func (f *FSM) Close() error {
	f.readMu.Lock()
	defer f.readMu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if err := f.db.Close(); err != nil {
		return fmt.Errorf("fsm: close: %w", err)
	}
	return nil
}

// Applied returns the index of the last command reflected in the state. It
// is safe to call from any goroutine. Unlike raft's AppliedIndex, which
// advances when an entry is handed to the state machine, it advances only
// once the entry's effect is in the database.
func (f *FSM) Applied() uint64 { return f.applied.Load() }

// Apply implements raft.FSM. It decodes a Command, applies it atomically and
// returns an *Outcome. Entries at or below the persisted applied index are
// already reflected in the state and are skipped.
//
// Apply never panics. Every input is either applied or rejected with an error
// Outcome; a rejected entry leaves the visible state unchanged and still
// advances the applied index, so every replica takes the same branch. Handlers
// validate their commands before they write. A panic that a handler still
// raises is a bug: it is recovered, counted (see PanicCount), and rejected the
// same way on every replica, so one bad entry cannot split the cluster.
func (f *FSM) Apply(l *raft.Log) (out interface{}) {
	if err := f.failure(); err != nil {
		return &Outcome{Err: err}
	}
	if h := f.applyHook.Load(); h != nil {
		(*h)(l.Index)
	}
	if l.Index <= f.applied.Load() {
		return &Outcome{}
	}
	defer func() {
		if r := recover(); r != nil {
			f.panics.Add(1)
			out = f.commitOutcome(l.Index, nil, errPanicked)
		}
	}()
	var cmd pb.Command
	if err := cmd.UnmarshalVT(l.Data); err != nil {
		return f.commitOutcome(l.Index, nil, fmt.Errorf("fsm: decode command %d: %w", l.Index, err))
	}
	b := f.db.NewBatch()
	defer func() { _ = b.Close() }()
	res, err := f.dispatch(b, &cmd, l.Index)
	if err != nil {
		// Commands validate before writing, but discard any partial batch
		// anyway so that a rejected command never changes the state.
		b.Reset()
	}
	return f.commitOutcome(l.Index, b, err, res)
}

// PanicCount reports how many entries or reads panicked and were rejected by
// the recovery in Apply and Read. It is zero for a correct state machine, and
// fuzzing asserts that it stays zero.
func (f *FSM) PanicCount() int64 { return f.panics.Load() }

// commitOutcome persists the applied index in batch b (creating one when
// nil), commits it, and builds the Outcome. A commit failure is fatal.
func (f *FSM) commitOutcome(index uint64, b *pebble.Batch, appErr error, res ...*pb.ApplyResult) *Outcome {
	if b == nil {
		b = f.db.NewBatch()
		defer func() { _ = b.Close() }()
	}
	if err := setUint64(b, keys.Meta(metaApplied), index); err != nil {
		return f.fail(err)
	}
	if err := b.Commit(pebble.NoSync); err != nil {
		return f.fail(fmt.Errorf("fsm: commit %d: %w", index, err))
	}
	f.applied.Store(index)
	out := &Outcome{Err: appErr}
	if appErr == nil && len(res) > 0 {
		out.Result = res[0]
	}
	return out
}

// dispatch applies one command into batch b.
func (f *FSM) dispatch(b *pebble.Batch, cmd *pb.Command, index uint64) (*pb.ApplyResult, error) {
	switch c := cmd.GetCmd().(type) {
	case *pb.Command_PutObject:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.putObject(b, c.PutObject) })
	case *pb.Command_DeleteObject:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.deleteObject(b, c.DeleteObject) })
	case *pb.Command_UpdateLayout:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.updateLayout(b, c.UpdateLayout) })
	case *pb.Command_UpdateTags:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.updateTags(b, c.UpdateTags) })
	case *pb.Command_CreateUpload:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.createUpload(b, c.CreateUpload) })
	case *pb.Command_PutPart:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.putPart(b, c.PutPart) })
	case *pb.Command_CompleteUpload:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.completeUpload(b, c.CompleteUpload) })
	case *pb.Command_AbortUpload:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.abortUpload(b, c.AbortUpload) })
	case *pb.Command_FenceBucket:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.fenceBucket(b, c.FenceBucket) })
	case *pb.Command_SetPartitionVersioning:
		return f.withKind(KindData, func() (*pb.ApplyResult, error) { return f.setPartitionVersioning(b, c.SetPartitionVersioning) })
	case *pb.Command_CreateBucket:
		return f.withKind(KindSystem, func() (*pb.ApplyResult, error) { return f.createBucket(b, c.CreateBucket) })
	case *pb.Command_MarkBucketDeleting:
		return f.withKind(KindSystem, func() (*pb.ApplyResult, error) { return f.markBucketDeleting(b, c.MarkBucketDeleting) })
	case *pb.Command_DeleteBucket:
		return f.withKind(KindSystem, func() (*pb.ApplyResult, error) { return f.deleteBucket(b, c.DeleteBucket) })
	case *pb.Command_SetBucketVersioning:
		return f.withKind(KindSystem, func() (*pb.ApplyResult, error) { return f.setBucketVersioning(b, c.SetBucketVersioning) })
	case *pb.Command_KvPut:
		return f.withKind(KindSystem, func() (*pb.ApplyResult, error) { return f.kvPut(b, c.KvPut, index) })
	case *pb.Command_KvDelete:
		return f.withKind(KindSystem, func() (*pb.ApplyResult, error) { return f.kvDelete(b, c.KvDelete) })
	default:
		return nil, fmt.Errorf("fsm: unknown command %T at %d", c, index)
	}
}

// withKind runs fn when the state machine accepts the command family.
func (f *FSM) withKind(want Kind, fn func() (*pb.ApplyResult, error)) (*pb.ApplyResult, error) {
	if f.kind != want {
		return nil, errWrongGroup
	}
	return fn()
}

// failure reports the fatal error, if the state machine has failed.
func (f *FSM) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.fatal
}

// fail records a fatal storage error and returns the outcome for it.
func (f *FSM) fail(err error) *Outcome {
	f.mu.Lock()
	if f.fatal == nil {
		f.fatal = fmt.Errorf("%w: %v", ErrFailed, err)
	}
	fatal := f.fatal
	f.mu.Unlock()
	return &Outcome{Err: fatal}
}

// errWrongGroup is returned when a command reaches the wrong kind of group.
// It indicates a routing bug, not a client error.
var errWrongGroup = errors.New("fsm: command sent to the wrong group")

// Metadata keys stored under keys.Meta.
const (
	metaApplied        = "applied"
	metaLastVersionTS  = "lastVersionTS"
	metaBucketSequence = "bucketSequence"
)
