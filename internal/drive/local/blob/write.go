package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
)

// copyExact reads exactly size bytes from r in chunks of at most len(buf)
// bytes and passes each chunk, with its file offset, to sink. It fails if r
// ends early, holds more bytes, or returns an error. The sink must not retain
// its argument.
func copyExact(ctx context.Context, buf []byte, r io.Reader, size int64, sink func(p []byte, off int64) error) error {
	var off int64
	for off < size {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(int64(len(buf)), size-off)
		chunk := buf[:n]
		if _, err := io.ReadFull(r, chunk); err != nil {
			return fmt.Errorf("blob: reading shard at %d: %w", off, err)
		}
		if err := sink(chunk, off); err != nil {
			return err
		}
		off += n
	}
	var one [1]byte
	n, err := r.Read(one[:])
	if n > 0 {
		return fmt.Errorf("blob: reader holds more than %d bytes", size)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("blob: reading shard: %w", err)
	}
	return nil
}

// writeBuffered writes through the page cache. It is the fallback where
// O_DIRECT is unavailable, and the only path on platforms without it.
func writeBuffered(ctx context.Context, pool *bufpool.Pool, path string, r io.Reader, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("blob: create %s: %w", path, err)
	}
	buf, err := pool.Get(ctx)
	if err != nil {
		_ = f.Close()
		return err
	}
	defer buf.Release()
	err = copyExact(ctx, buf.Bytes(), r, size, func(p []byte, off int64) error {
		_, werr := f.WriteAt(p, off)
		return werr
	})
	if err == nil {
		err = syncerr.Wrap("fsync", path, f.Sync())
	}
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	return err
}
