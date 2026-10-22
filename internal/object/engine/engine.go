package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/semaphore"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine/gc"
)

// Engine implements object.Layer on a metadata store and a registry of
// erasure sets. It is safe for concurrent use.
//
// Call Close when done. Close stops the background deletion queue.
type Engine struct {
	store      meta.Store
	sets       erasure.Registry
	inline     int64
	clock      func() time.Time
	logger     *slog.Logger
	gc         *gc.Queue
	inlinePool *bufpool.Pool
	writes     *semaphore.Weighted
	deleteConc int
}

var _ object.Layer = (*Engine)(nil)

// New returns an engine over store and sets. It starts the deletion queue's
// workers, so the caller must Close it.
func New(store meta.Store, sets erasure.Registry, opts Options) (*Engine, error) {
	if store == nil {
		return nil, fmt.Errorf("engine: nil metadata store")
	}
	if sets == nil {
		return nil, fmt.Errorf("engine: nil erasure registry")
	}
	r, err := opts.resolve()
	if err != nil {
		return nil, err
	}
	queue, err := gc.New(r.gc)
	if err != nil {
		return nil, fmt.Errorf("engine: %w", err)
	}
	budget := bufpool.NewBudget(r.bufferBudget)
	return &Engine{
		store:      store,
		sets:       sets,
		inline:     r.inlineThreshold,
		clock:      r.clock,
		logger:     r.logger,
		gc:         queue,
		inlinePool: bufpool.NewPool(int(r.inlineThreshold), budget),
		writes:     semaphore.NewWeighted(int64(r.maxConcurrentWrites)),
		deleteConc: r.deleteConcurrency,
	}, nil
}

// Flush waits until every queued data deletion has finished or ctx is done.
// Deletions are asynchronous, so data of a replaced or deleted version may
// still be on disk until Flush returns.
func (e *Engine) Flush(ctx context.Context) error {
	return e.gc.Wait(ctx)
}

// Close stops accepting data deletions and waits for queued ones to finish,
// or until ctx is done. It does not close the store or the registry.
func (e *Engine) Close(ctx context.Context) error {
	return e.gc.Close(ctx)
}

// now returns the current time from the configured clock.
func (e *Engine) now() time.Time {
	return e.clock()
}

// reclaim queues the data of garbage versions for deletion. It is called only
// after the metadata change that made them garbage has committed (I5).
func (e *Engine) reclaim(garbage []meta.ObjectRecord) {
	for i := range garbage {
		e.reclaimParts(garbage[i].Parts)
	}
}

// reclaimParts queues the data of parts for deletion.
func (e *Engine) reclaimParts(parts []meta.PartRecord) {
	for _, p := range parts {
		if p.Data.IsZero() {
			continue
		}
		set, err := e.sets.Get(p.Layout.Set)
		if err != nil {
			e.logger.Warn("engine: cannot reach the erasure set of garbage data; the scrubber reclaims it",
				"data", p.Data.String(), "err", err)
			continue
		}
		e.gc.Enqueue(gc.Task{Set: set, Data: p.Data, Layout: p.Layout})
	}
}

// acquireWrite takes a slot for one erasure-coded write. The caller must call
// the returned function when the write has finished.
func (e *Engine) acquireWrite(ctx context.Context) (func(), error) {
	if err := e.writes.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("engine: waiting for a write slot: %w", err)
	}
	return func() { e.writes.Release(1) }, nil
}

// checkCtx reports a cancelled or expired context before any work starts.
func checkCtx(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("engine: %w", err)
	}
	return nil
}

// newID returns a random 128-bit identifier as 32 lower-case hex characters.
// It is used for upload ids and version ids, never for on-disk names.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("engine: random id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// infoOf describes a stored version. latest reports whether it is the
// current version of its key.
func infoOf(rec meta.ObjectRecord, latest bool) object.Info {
	info := rec.Info()
	info.IsLatest = latest
	return info
}

// partInfo describes a stored part.
func partInfo(p meta.PartRecord) object.PartInfo {
	return object.PartInfo{
		PartNumber: p.Number,
		Size:       p.Size,
		ETag:       p.ETag,
		ModTime:    p.ModTime.UnixNano(),
		Checksum:   p.Checksum,
	}
}

// versionFor returns the version id for a new version of bucket: a fresh id
// in versioning-enabled buckets, the null version otherwise.
func (e *Engine) versionFor(ctx context.Context, bucket string) (string, error) {
	info, err := e.store.GetBucket(ctx, bucket)
	if err != nil {
		return "", err
	}
	if info.Versioning != object.VersioningEnabled {
		return "", nil
	}
	return newID()
}
