package blob

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/eukalpia/unio/internal/bufpool"
)

// copyFromPool copies exactly size bytes from r to sink, like copyExact, with
// buffers from pool.
//
// When a second buffer is free and the blob spans more than one chunk, reading
// and writing overlap: the next chunk is read from r while sink writes the
// previous one. With O_DIRECT every write waits for the device, so without
// the overlap the producer and the device take turns and their latencies add
// up. The second buffer is taken without waiting, so writers never hold one
// buffer while waiting for another and cannot deadlock on a shared budget:
// under memory pressure the copy falls back to one buffer.
func copyFromPool(ctx context.Context, pool *bufpool.Pool, r io.Reader, size int64, sink func(p []byte, off int64) error) error {
	first, err := pool.Get(ctx)
	if err != nil {
		return err
	}
	defer first.Release()
	if size <= int64(len(first.Bytes())) {
		return copyExact(ctx, first.Bytes(), r, size, sink)
	}
	second, ok := pool.TryGet()
	if !ok {
		return copyExact(ctx, first.Bytes(), r, size, sink)
	}
	defer second.Release()
	return copyOverlapped(ctx, [2][]byte{first.Bytes(), second.Bytes()}, r, size, sink)
}

// filledChunk is one filled buffer on its way to the sink.
type filledChunk struct {
	buf int // index into the buffer pair
	n   int
	off int64
}

// copyOverlapped is copyExact with two buffers. One goroutine calls sink, in
// offset order, while the caller reads the next chunk. The contract is that of
// copyExact: exactly size bytes reach sink, and a short, long or failing
// reader is an error. A sink error stops the reading at the next chunk and is
// returned in preference to any read error it caused.
func copyOverlapped(ctx context.Context, bufs [2][]byte, r io.Reader, size int64, sink func(p []byte, off int64) error) error {
	filled := make(chan filledChunk, 1)
	free := make(chan int, 2)
	free <- 0
	free <- 1
	var sinkFailed atomic.Bool
	sinkErr := make(chan error, 1)
	go func() {
		var err error
		for c := range filled {
			if err == nil {
				if err = sink(bufs[c.buf][:c.n], c.off); err != nil {
					sinkFailed.Store(true)
				}
			}
			free <- c.buf
		}
		sinkErr <- err
	}()

	readErr := func() error {
		var off int64
		for off < size {
			if err := ctx.Err(); err != nil {
				return err
			}
			if sinkFailed.Load() {
				return nil
			}
			var i int
			select {
			case i = <-free:
			case <-ctx.Done():
				return ctx.Err()
			}
			n := min(int64(len(bufs[i])), size-off)
			if _, err := io.ReadFull(r, bufs[i][:n]); err != nil {
				return fmt.Errorf("blob: reading shard at %d: %w", off, err)
			}
			filled <- filledChunk{buf: i, n: int(n), off: off}
			off += n
		}
		return checkDrained(r, size)
	}()
	close(filled)
	if err := <-sinkErr; err != nil {
		return err
	}
	return readErr
}
