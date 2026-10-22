package rs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

// Heal implements erasure.Set. It reads every shard in full and verifies it.
// Each shard that is missing, corrupt or unreadable is removed, rebuilt from
// the intact shards, and written again: to the drive the layout records, or
// to the set's drive at that index when the layout records none.
//
// The returned layout records the drives of the shards that were rewritten.
// If some shards could not be rewritten, the error reports them and the
// layout records them as missing.
func (s *Set) Heal(ctx context.Context, id ids.DataID, layout erasure.Layout, size int64) (erasure.Layout, error) {
	g, err := s.layoutGeom(layout)
	if err != nil {
		return layout, err
	}
	if size < 0 {
		return layout, fmt.Errorf("erasure/rs: heal %s: negative size %d", id, size)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	intact, err := s.verifyAll(ctx, g, layout, id, size)
	if err != nil {
		return layout, fmt.Errorf("erasure/rs: heal %s: %w", id, err)
	}
	var damaged, sources []int
	for i, ok := range intact {
		if ok {
			sources = append(sources, i)
		} else {
			damaged = append(damaged, i)
		}
	}
	if len(damaged) == 0 {
		return layout, nil
	}
	if len(sources) < s.k {
		return layout, fmt.Errorf("erasure/rs: heal %s: %d of %d shards intact: %w",
			id, len(sources), s.k, erasure.ErrReadQuorum)
	}

	out := layout
	out.Drives = slices.Clone(layout.Drives)
	var errs []error
	refs := make([]shardRef, len(damaged))
	for j, i := range damaged {
		d := s.shardDrive(layout, i)
		if d == nil {
			d = s.drives[i]
		}
		refs[j] = shardRef{d: d, id: shardID(id, i)}
	}
	var live []*target
	for j, derr := range deleteShards(ctx, refs) {
		i := damaged[j]
		if derr != nil {
			// The stale shard is still there, so the layout keeps its entry.
			errs = append(errs, derr)
			continue
		}
		out.Drives[i] = ids.DriveID{}
		live = append(live, newTarget(i, refs[j].d, refs[j].id, bitrot.ShardSize(size, s.k, g.stripe)))
	}
	if len(live) > 0 {
		errs = append(errs, s.rewrite(ctx, cancel, g, layout, id, size, live, sources, &out)...)
	}
	if err := errors.Join(errs...); err != nil {
		return out, fmt.Errorf("erasure/rs: heal %s: %w", id, err)
	}
	return out, nil
}

// rewrite rebuilds the live targets from the source shards, stripe by stripe,
// and records the drive of each target that was written in out. It returns
// the failures.
func (s *Set) rewrite(ctx context.Context, cancel context.CancelFunc, g *geom, layout erasure.Layout,
	id ids.DataID, size int64, live []*target, sources []int, out *erasure.Layout) []error {
	fo := startFanout(ctx, s.opts.StallTimeout, live)
	required := make([]bool, s.k+s.m)
	for _, t := range live {
		required[t.index] = true
	}
	streams := s.streamsFor(layout, id, size, g.stripe, bitrot.Stripes(size, g.stripe))
	rerr := s.rebuild(ctx, g, streams, sources, required, fo, size)
	if rerr != nil {
		cancel()
	}
	fo.finish()
	for _, st := range streams {
		st.close()
	}
	var errs []error
	for _, t := range live {
		if t.written() {
			out.Drives[t.index] = t.drive.ID()
		} else {
			errs = append(errs, t.failure())
		}
	}
	if rerr != nil {
		errs = append(errs, rerr)
	}
	return errs
}

// rebuild reconstructs the required shards of every stripe from the source
// shards and dispatches their blocks to fo.
func (s *Set) rebuild(ctx context.Context, g *geom, streams []*stream, sources []int, required []bool, fo *fanout, size int64) error {
	for st := range bitrot.Stripes(size, g.stripe) {
		f, err := newFrame(ctx, g, s.k+s.m)
		if err != nil {
			return err
		}
		f.setStripe(st, bitrot.BlockLen(size, s.k, g.stripe, st), int(bitrot.StripeLen(size, g.stripe, st)))
		f.reset()
		ok, _, _ := gather(ctx, streams, sources, st, f, s.k)
		if len(ok) < s.k {
			f.release()
			return fmt.Errorf("erasure/rs: heal stripe %d: %d of %d sources readable: %w",
				st, len(ok), s.k, erasure.ErrReadQuorum)
		}
		if err := s.enc.ReconstructSome(f.shards, required); err != nil {
			f.release()
			return fmt.Errorf("erasure/rs: reconstruct stripe %d: %w", st, err)
		}
		if err := fo.dispatch(f); err != nil {
			return err
		}
	}
	return nil
}

// verifyAll reads every shard of object id in full and reports which are
// intact: present on a drive, with every block verifying and no data beyond
// the framing.
func (s *Set) verifyAll(ctx context.Context, g *geom, layout erasure.Layout, id ids.DataID, size int64) ([]bool, error) {
	intact := make([]bool, s.k+s.m)
	var wg sync.WaitGroup
	for i := range intact {
		d := s.shardDrive(layout, i)
		if d == nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			intact[i] = verifyShard(ctx, s.k, g, d, shardID(id, i), size)
		}()
	}
	wg.Wait()
	return intact, ctx.Err()
}

// verifyShard reports whether the whole shard sid on d verifies. It reads one
// block at a time through a single pooled block buffer.
func verifyShard(ctx context.Context, k int, g *geom, d drive.Drive, sid ids.ShardID, size int64) bool {
	rc, err := d.ReadShard(ctx, sid, 0, -1)
	if err != nil {
		return false
	}
	defer func() { _ = rc.Close() }()
	buf, err := g.blocks.Get(ctx)
	if err != nil {
		return false
	}
	defer buf.Release()
	br := bitrot.NewReader(rc)
	for st := range bitrot.Stripes(size, g.stripe) {
		if br.ReadBlock(buf.Bytes()[:bitrot.BlockLen(size, k, g.stripe, st)]) != nil {
			return false
		}
	}
	return br.Done() == nil
}
