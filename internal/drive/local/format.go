package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// formatVersion is the version of the on-disk layout written by this package.
const formatVersion = 1

// Errors returned by Format, FormatWithID, EnsureFormatted and Open for the
// drive's identity.
var (
	// ErrAlreadyFormatted is returned by Format for a directory that already
	// holds a drive.
	ErrAlreadyFormatted = errors.New("local: drive already formatted")
	// ErrNotFormatted is returned by Open for a directory without a format
	// record.
	ErrNotFormatted = errors.New("local: drive not formatted")
	// ErrClusterMismatch is returned by Open for a drive that belongs to a
	// different cluster than Options.ClusterID.
	ErrClusterMismatch = errors.New("local: drive belongs to another cluster")
	// ErrDriveIDMismatch is returned for a drive whose format record names a
	// different drive id than the one expected. The drive was moved to another
	// slot, or its paths were swapped.
	ErrDriveIDMismatch = errors.New("local: drive has another id")
)

// Format prepares the directory at path as a drive of clusterID and returns
// its new drive id. The format record is written atomically, so a crash
// leaves either no record or a complete one.
func Format(ctx context.Context, path, clusterID string) (ids.DriveID, error) {
	id := ids.NewDriveID()
	if err := FormatWithID(ctx, path, clusterID, id); err != nil {
		return ids.DriveID{}, err
	}
	return id, nil
}

// FormatWithID prepares the directory at path as a drive of clusterID with the
// given drive id. The id must not be zero. A directory that already holds a
// drive fails with ErrAlreadyFormatted and is not changed. The record is
// written as Format writes it.
func FormatWithID(ctx context.Context, path, clusterID string, id ids.DriveID) error {
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
	return formatLocked(lay, clusterID, id)
}

// checkFormatArgs validates the arguments of FormatWithID and EnsureFormatted.
func checkFormatArgs(ctx context.Context, clusterID string, id ids.DriveID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if clusterID == "" {
		return errors.New("local: cluster id must not be empty")
	}
	if id.IsZero() {
		return errors.New("local: drive id must not be zero")
	}
	return nil
}

// formatLocked writes the format record of lay. The caller holds the format
// lock of lay.format, so the check for an existing record and the write are
// not interleaved with another formatter in this process.
func formatLocked(lay layout, clusterID string, id ids.DriveID) error {
	if _, err := os.Stat(lay.format); err == nil {
		return ErrAlreadyFormatted
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("local: check format: %w", err)
	}
	for _, dir := range append(lay.dirs(), filepath.Dir(lay.index)) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("local: create %s: %w", dir, err)
		}
	}
	data, err := proto.Marshal(&uniov1.DriveFormat{
		FormatVersion:   formatVersion,
		ClusterId:       clusterID,
		DriveId:         id[:],
		CreatedUnixNano: time.Now().UnixNano(),
	})
	if err != nil {
		return fmt.Errorf("local: encode format: %w", err)
	}
	return writeFileAtomic(lay.format, data)
}

// formatRecord is the decoded format record of a drive.
type formatRecord struct {
	clusterID string
	id        ids.DriveID
}

// loadFormat reads and decodes the format record of lay. It returns
// ErrNotFormatted when there is no record.
func loadFormat(lay layout) (formatRecord, error) {
	data, err := os.ReadFile(lay.format) // #nosec G304 -- path is the drive root joined with a fixed name, never user input
	if errors.Is(err, os.ErrNotExist) {
		return formatRecord{}, ErrNotFormatted
	}
	if err != nil {
		return formatRecord{}, fmt.Errorf("local: read format: %w", err)
	}
	var rec uniov1.DriveFormat
	if err := proto.Unmarshal(data, &rec); err != nil {
		return formatRecord{}, fmt.Errorf("local: decode format: %w", err)
	}
	if rec.FormatVersion != formatVersion {
		return formatRecord{}, fmt.Errorf("local: unsupported format version %d", rec.FormatVersion)
	}
	if len(rec.DriveId) != len(ids.DriveID{}) {
		return formatRecord{}, errors.New("local: format record has a malformed drive id")
	}
	var id ids.DriveID
	copy(id[:], rec.DriveId)
	return formatRecord{clusterID: rec.ClusterId, id: id}, nil
}

// verify checks the record against the cluster and drive id a caller expects.
// An empty clusterID or a zero want is not checked. The cluster is checked
// first, so a drive from another cluster is reported as such.
func (r formatRecord) verify(clusterID string, want ids.DriveID) error {
	if clusterID != "" && r.clusterID != clusterID {
		return ErrClusterMismatch
	}
	if !want.IsZero() && r.id != want {
		return fmt.Errorf("%w: format record names %s, expected %s", ErrDriveIDMismatch, r.id, want)
	}
	return nil
}

// writeFileAtomic replaces path with data. It writes a temporary file, syncs
// it, renames it over path and syncs the directory.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- path is the drive root joined with a fixed name, never user input
	if err != nil {
		return fmt.Errorf("local: create %s: %w", tmp, err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("local: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("local: rename %s: %w", path, err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("local: sync %s: %w", filepath.Dir(path), err)
	}
	return nil
}
