package rs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/bitrot"
	"github.com/eukalpia/unio/internal/ids"
)

var (
	errNoDrive    = errors.New("shard has no drive in this set")
	errStreamDead = errors.New("shard stream is no longer usable")
)

// stream reads the blocks of one shard in stripe order. It opens the drive
// stream lazily at the stripe it is asked for and reopens it when a caller
// skips stripes. A stream is used by one goroutine at a time.
type stream struct {
	index  int
	shard  ids.ShardID
	drive  drive.Drive // nil when the shard has no drive in the set
	size   int64       // object size
	k      int
	stripe int   // stripe size of the object
	end    int64 // stripes are read up to, not including, end
	rc     io.ReadCloser
	br     *bitrot.Reader
	next   int64 // stripe the open stream is positioned at
	dead   bool  // no further reads in this operation
}

// streamsFor returns one stream per shard of object id, for stripes up to end.
func (s *Set) streamsFor(layout erasure.Layout, id ids.DataID, size int64, stripe int, end int64) []*stream {
	out := make([]*stream, s.k+s.m)
	for i := range out {
		out[i] = &stream{
			index:  i,
			shard:  shardID(id, i),
			drive:  s.shardDrive(layout, i),
			size:   size,
			k:      s.k,
			stripe: stripe,
			end:    end,
		}
	}
	return out
}

// close releases the drive stream, if one is open.
func (st *stream) close() {
	if st.rc != nil {
		_ = st.rc.Close()
		st.rc, st.br = nil, nil
	}
}

// readBlock reads and verifies the block of stripe s into dst, whose length
// is the block length of that stripe. A bitrot mismatch leaves the stream
// usable for the next stripe; any other failure makes it dead.
func (st *stream) readBlock(ctx context.Context, s int64, dst []byte) error {
	if st.dead {
		return errStreamDead
	}
	if st.drive == nil {
		st.dead = true
		return fmt.Errorf("shard %d: %w", st.index, errNoDrive)
	}
	if st.rc == nil || st.next != s {
		st.close()
		ext, err := bitrot.StripeExtent(st.size, st.k, st.stripe, s, st.end)
		if err != nil {
			st.dead = true
			return fmt.Errorf("shard %d: %w", st.index, err)
		}
		rc, err := st.drive.ReadShard(ctx, st.shard, ext.Offset, ext.Length)
		if err != nil {
			st.dead = true
			return fmt.Errorf("shard %d: open at stripe %d: %w", st.index, s, err)
		}
		st.rc, st.br, st.next = rc, bitrot.NewReader(rc, ext.Position(st.shard)), s
	}
	err := st.br.ReadBlock(dst)
	st.next++
	if st.br.Err() != nil {
		st.dead = true
		st.close()
	}
	if err != nil {
		return fmt.Errorf("shard %d stripe %d: %w", st.index, s, err)
	}
	return nil
}

// gather reads the block of stripe s into f from the candidate streams, in
// order, until want blocks verify or the candidates run out. Candidates are
// read in parallel batches sized to the blocks still needed. It returns the
// indices that verified, the indices that failed, and whether any failure was
// a bitrot mismatch. Failed shards are left missing in f.
func gather(ctx context.Context, streams []*stream, cand []int, s int64, f *frame, want int) (ok, failed []int, corrupt bool) {
	for next := 0; len(ok) < want && next < len(cand); {
		var batch []int
		for ; next < len(cand) && len(batch) < want-len(ok); next++ {
			if !streams[cand[next]].dead {
				batch = append(batch, cand[next])
			}
		}
		if len(batch) == 0 {
			break
		}
		errs := readBatch(ctx, streams, batch, s, f)
		for j, idx := range batch {
			if errs[j] == nil {
				ok = append(ok, idx)
				continue
			}
			failed = append(failed, idx)
			f.shards[idx] = f.slots[idx][:0]
			corrupt = corrupt || errors.Is(errs[j], erasure.ErrCorrupt)
		}
	}
	return ok, failed, corrupt
}

// readBatch reads block s of each shard in batch into f, in parallel, and
// returns one error per shard.
func readBatch(ctx context.Context, streams []*stream, batch []int, s int64, f *frame) []error {
	errs := make([]error, len(batch))
	for _, idx := range batch {
		f.shards[idx] = f.slots[idx][:f.bl]
	}
	if len(batch) == 1 {
		errs[0] = streams[batch[0]].readBlock(ctx, s, f.shards[batch[0]])
		return errs
	}
	var wg sync.WaitGroup
	for j, idx := range batch {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[j] = streams[idx].readBlock(ctx, s, f.shards[idx])
		}()
	}
	wg.Wait()
	return errs
}

// indexRange returns the integers [lo, hi).
func indexRange(lo, hi int) []int {
	out := make([]int, 0, hi-lo)
	for i := lo; i < hi; i++ {
		out = append(out, i)
	}
	return out
}
