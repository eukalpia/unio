package syncerr

import (
	"errors"
	"fmt"
	"io/fs"
	"syscall"
	"testing"
)

func TestWrapNil(t *testing.T) {
	if err := Wrap("fsync", "/d/seg", nil); err != nil {
		t.Fatalf("Wrap(nil) = %v, want nil", err)
	}
}

// TestIsThroughLayers checks that the marker survives the wrapping the drive
// layers add on the way up, since a missed marker would let a failed fsync be
// retried and a write be acknowledged without being durable.
func TestIsThroughLayers(t *testing.T) {
	err := Wrap("fdatasync", "/d/seg/000001", syscall.EIO)
	for _, wrapped := range []error{
		err,
		fmt.Errorf("segment: commit: %w", err),
		fmt.Errorf("drive: %w", fmt.Errorf("group: %w", err)),
		errors.Join(errors.New("other"), err),
	} {
		if !Is(wrapped) {
			t.Fatalf("Is(%v) = false", wrapped)
		}
		if !errors.Is(wrapped, syscall.EIO) {
			t.Fatalf("underlying errno lost in %v", wrapped)
		}
	}
	if got := err.Error(); got != "fdatasync /d/seg/000001: input/output error" {
		t.Fatalf("Error() = %q", got)
	}
}

// TestIsOnlyMarked checks that ordinary I/O failures are not mistaken for a
// failed sync, which would poison a healthy drive.
func TestIsOnlyMarked(t *testing.T) {
	for _, err := range []error{
		nil,
		syscall.EIO,
		&fs.PathError{Op: "write", Path: "/d/seg", Err: syscall.ENOSPC},
		fmt.Errorf("fsync failed: %v", syscall.EIO), // text only, not wrapped
	} {
		if Is(err) {
			t.Fatalf("Is(%v) = true", err)
		}
	}
}
