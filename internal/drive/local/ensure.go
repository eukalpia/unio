package local

import (
	"context"
	"errors"

	"github.com/eukalpia/unio/internal/ids"
)

// EnsureFormatted makes the directory at path a drive of clusterID with the
// given drive id. An unformatted directory is formatted as FormatWithID formats
// it. A formatted directory is not changed: its record must name the same
// cluster and drive id, or EnsureFormatted returns ErrClusterMismatch or an
// error that wraps ErrDriveIDMismatch. The check and the write happen under
// the format lock of path, so concurrent callers in one process see one
// record.
func EnsureFormatted(ctx context.Context, path, clusterID string, id ids.DriveID) error {
	if err := checkFormatArgs(ctx, clusterID, id); err != nil {
		return err
	}
	lay, err := newLayout(path)
	if err != nil {
		return err
	}
	unlock, err := lockFormat(ctx, lay.format)
	if err != nil {
		return err
	}
	defer unlock()

	rec, err := loadFormat(lay)
	if errors.Is(err, ErrNotFormatted) {
		return formatLocked(lay, clusterID, id)
	}
	if err != nil {
		return err
	}
	return rec.verify(clusterID, id)
}
