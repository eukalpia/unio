//go:build !linux

package blob

import (
	"context"
	"io"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive/local/internal/faults"
)

// writeFile writes a blob through the page cache. O_DIRECT is Linux-only.
func writeFile(ctx context.Context, pool *bufpool.Pool, path string, r io.Reader, size int64, hooks *faults.Hooks) error {
	return writeBuffered(ctx, pool, path, r, size, hooks)
}
