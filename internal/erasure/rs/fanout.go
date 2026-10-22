package rs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

// queueDepth is the number of stripes buffered per target between the
// dispatcher and the target's feeder. Together with the frames held in
// flight it bounds the memory of a fan-out.
const queueDepth = 2

// errStalled reports a target that did not accept a block within the stall
// timeout.
var errStalled = errors.New("drive stalled")

// target is one shard being written to one drive by a fanout.
type target struct {
	index int
	drive drive.Drive
	shard ids.ShardID
	size  int64 // framed shard size

	ch     chan *frame   // stripes awaiting the feeder, each holding one reference
	done   chan struct{} // closed when the driver returns
	cancel context.CancelFunc

	// err and feedErr are written before the goroutines finish and read only
	// after they have all returned. dead is owned by the dispatcher.
	err     error
	feedErr error
	dead    bool
}

// newTarget describes the write of shard sid to d, of framed size size.
func newTarget(index int, d drive.Drive, sid ids.ShardID, size int64) *target {
	return &target{index: index, drive: d, shard: sid, size: size}
}

// written reports whether the target stored its whole shard.
func (t *target) written() bool {
	return t.err == nil && t.feedErr == nil && !t.dead
}

// failure returns why a target did not write its shard, or nil.
func (t *target) failure() error {
	switch {
	case t.err != nil:
		return fmt.Errorf("shard %d on drive %s: %w", t.index, t.drive.ID(), t.err)
	case t.feedErr != nil:
		return fmt.Errorf("shard %d on drive %s: %w", t.index, t.drive.ID(), t.feedErr)
	case t.dead:
		return fmt.Errorf("shard %d on drive %s: %w", t.index, t.drive.ID(), errStalled)
	}
	return nil
}

// fanout writes one shard per target from one stream of stripes. Each target
// runs two goroutines: a feeder that frames blocks with bitrot onto a pipe,
// and a driver that calls drive.WriteShard reading that pipe. The dispatcher
// sends each stripe to every target through a bounded channel.
//
// A target whose driver has returned, or that does not accept a block within
// the stall timeout, is dropped: the dispatcher stops feeding it and the other
// targets continue. Because the pipe is closed when the write context ends, a
// driver blocked on its reader always returns.
//
// fanout is used by one dispatcher goroutine. The targets' drives must return
// from WriteShard when the context they were given is cancelled.
type fanout struct {
	ctx     context.Context
	stall   time.Duration
	targets []*target
	wg      sync.WaitGroup
}

// startFanout starts the goroutines of every target. The caller must call
// finish exactly once.
func startFanout(ctx context.Context, stall time.Duration, targets []*target) *fanout {
	fo := &fanout{ctx: ctx, stall: stall, targets: targets}
	for _, t := range targets {
		tctx, cancel := context.WithCancel(ctx)
		t.cancel = cancel
		t.ch = make(chan *frame, queueDepth)
		t.done = make(chan struct{})
		pr, pw := io.Pipe()
		stop := context.AfterFunc(tctx, func() {
			_ = pr.CloseWithError(context.Cause(tctx))
		})
		fo.wg.Add(2)
		go fo.runDriver(t, tctx, pr, stop)
		go fo.runFeeder(t, pw)
	}
	return fo
}

// drive runs drive.WriteShard for t. The pipe reader is closed with the
// driver's result, which unblocks the feeder.
func (fo *fanout) runDriver(t *target, tctx context.Context, pr *io.PipeReader, stop func() bool) {
	defer fo.wg.Done()
	err := t.drive.WriteShard(tctx, t.shard, pr, t.size)
	stop()
	t.err = err
	if err != nil {
		_ = pr.CloseWithError(err)
	} else {
		_ = pr.Close()
	}
	close(t.done)
}

// feed frames each queued stripe's block for t onto the pipe. After the first
// write error it keeps draining the queue, releasing references, until the
// dispatcher closes it.
func (fo *fanout) runFeeder(t *target, pw *io.PipeWriter) {
	defer fo.wg.Done()
	bw := bitrot.NewWriter(pw, bitrot.Position{Data: t.shard.Data, Shard: t.shard.Index})
	var werr error
	for f := range t.ch {
		if werr == nil {
			werr = bw.WriteBlock(f.shards[t.index])
		}
		f.unref()
	}
	t.feedErr = werr
	_ = pw.CloseWithError(werr)
}

// dispatch sends the stripe in f to every target. Each target holds one
// reference: its feeder releases it after writing, or dispatch releases it
// for a target that was skipped. Every reference is released even when
// dispatch fails, so the frame's buffer always returns to its pool.
func (fo *fanout) dispatch(f *frame) error {
	f.refs.Store(int32(len(fo.targets)))
	var err error
	for _, t := range fo.targets {
		sent := false
		if err == nil {
			sent, err = fo.send(t, f)
		}
		if !sent {
			f.unref()
		}
	}
	return err
}

// send queues f for t. It reports whether the frame was queued. A target
// that is already done, or does not accept the frame within the stall
// timeout, is marked dead and cancelled.
func (fo *fanout) send(t *target, f *frame) (bool, error) {
	if t.dead {
		return false, nil
	}
	select {
	case <-t.done:
		t.dead = true
		return false, nil
	default:
	}
	select {
	case t.ch <- f:
		return true, nil
	default:
	}
	timer := time.NewTimer(fo.stall)
	defer timer.Stop()
	select {
	case t.ch <- f:
		return true, nil
	case <-t.done:
		t.dead = true
		return false, nil
	case <-timer.C:
		t.dead = true
		t.cancel()
		return false, nil
	case <-fo.ctx.Done():
		return false, fmt.Errorf("erasure/rs: write: %w", context.Cause(fo.ctx))
	}
}

// finish closes every target's queue and waits for all goroutines. After it
// returns, the outcome of each target is final.
func (fo *fanout) finish() {
	for _, t := range fo.targets {
		close(t.ch)
	}
	fo.awaitDrivers()
	for _, t := range fo.targets {
		t.cancel()
	}
	fo.wg.Wait()
}

// awaitDrivers waits for the drivers to return. A driver still running after
// the stall timeout has stalled: it is cancelled and marked dead, and the wait
// then covers only the cancellation, so a stuck drive cannot block the write.
func (fo *fanout) awaitDrivers() {
	timer := time.NewTimer(fo.stall)
	defer timer.Stop()
	for _, t := range fo.targets {
		select {
		case <-t.done:
			continue
		case <-timer.C:
		}
		for _, u := range fo.targets {
			select {
			case <-u.done:
			default:
				u.dead = true
				u.cancel()
			}
		}
		for _, u := range fo.targets {
			<-u.done
		}
		return
	}
}
