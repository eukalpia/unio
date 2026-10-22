package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"

	"github.com/eukalpia/unio/internal/bufpool"
)

// writeFile writes a blob with O_DIRECT, bypassing the page cache for the
// bulk of the data. Only whole 4096-byte blocks go through O_DIRECT. The
// final partial block, if any, is written through a buffered descriptor
// after the direct one is closed. Filesystems that reject O_DIRECT fall back
// to buffered writes.
func writeFile(ctx context.Context, pool *bufpool.Pool, path string, r io.Reader, size int64) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_DIRECT, 0o600)
	if err != nil {
		if errors.Is(err, unix.EINVAL) {
			return writeBuffered(ctx, pool, path, r, size)
		}
		return fmt.Errorf("blob: create %s: %w", path, err)
	}
	tailOff, tail, err := writeDirect(ctx, pool, f, r, size)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("blob: close %s: %w", path, cerr)
	}
	if err != nil {
		return err
	}
	return finishBuffered(path, tailOff, tail)
}

// writeDirect writes all whole blocks of the input through f. It returns the
// offset and bytes of the unaligned tail, which the caller must write.
func writeDirect(ctx context.Context, pool *bufpool.Pool, f *os.File, r io.Reader, size int64) (int64, []byte, error) {
	if err := unix.Fallocate(int(f.Fd()), 0, 0, size); err != nil &&
		!errors.Is(err, unix.EOPNOTSUPP) && !errors.Is(err, unix.ENOSYS) {
		return 0, nil, fmt.Errorf("blob: preallocate %d bytes: %w", size, err)
	}
	buf, err := pool.Get(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer buf.Release()

	var tail []byte
	var tailOff int64
	err = copyExact(ctx, buf.Bytes(), r, size, func(p []byte, off int64) error {
		whole := len(p) - len(p)%directAlign
		if whole > 0 {
			if _, err := f.WriteAt(p[:whole], off); err != nil {
				return fmt.Errorf("blob: direct write at %d: %w", off, err)
			}
		}
		if whole < len(p) {
			tail = append(tail[:0], p[whole:]...)
			tailOff = off + int64(whole)
		}
		return nil
	})
	if err != nil {
		return 0, nil, err
	}
	return tailOff, tail, nil
}

// finishBuffered writes the unaligned tail, if any, through a buffered
// descriptor and makes the whole file durable.
func finishBuffered(path string, off int64, tail []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("blob: reopen %s: %w", path, err)
	}
	if len(tail) > 0 {
		if _, err := f.WriteAt(tail, off); err != nil {
			_ = f.Close()
			return fmt.Errorf("blob: write tail of %s: %w", path, err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("blob: sync %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("blob: close %s: %w", path, err)
	}
	return nil
}
