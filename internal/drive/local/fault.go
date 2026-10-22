package local

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
)

// A failed fsync makes the drive faulty. Its writes can no longer be trusted
// to be durable: the kernel may have dropped the pages the failed fsync was
// meant to write, and a retried fsync can report success for data that is
// gone. So the drive records the first failure and refuses every mutation from
// then on. Reads keep working, and the drive is faulty until it is reopened.
// Reopening replays what is on disk. The failed fsync is never retried.

// markFaulty records cause as the drive's fault, if none is recorded yet, and
// returns the error callers see. It is safe to call from any goroutine.
func (d *Drive) markFaulty(cause error) error {
	d.faultMu.Lock()
	defer d.faultMu.Unlock()
	if d.fault == nil {
		d.fault = cause
		slog.Default().Error("drive is faulty: a durability sync failed; writes are refused until the drive is reopened",
			"drive", d.lay.root, "err", cause)
	}
	return fmt.Errorf("%w: %w", drive.ErrFaulty, d.fault)
}

// faultError returns the drive-faulty error, or nil while the drive is healthy.
func (d *Drive) faultError() error {
	d.faultMu.Lock()
	defer d.faultMu.Unlock()
	if d.fault == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", drive.ErrFaulty, d.fault)
}

// checkSync turns a failed fsync-class operation into a drive fault. Any other
// error is returned unchanged.
func (d *Drive) checkSync(err error) error {
	if err == nil || errors.Is(err, drive.ErrFaulty) {
		return err
	}
	if syncerr.Is(err) {
		return d.markFaulty(err)
	}
	return err
}
