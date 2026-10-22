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

// Errors returned by Format and Open for the drive's identity.
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
)

// Format prepares the directory at path as a drive of clusterID and returns
// its new drive id. The format record is written atomically, so a crash
// leaves either no record or a complete one.
func Format(ctx context.Context, path, clusterID string) (ids.DriveID, error) {
	if err := ctx.Err(); err != nil {
		return ids.DriveID{}, err
	}
	if clusterID == "" {
		return ids.DriveID{}, errors.New("local: cluster id must not be empty")
	}
	lay := newLayout(path)
	if _, err := os.Stat(lay.format); err == nil {
		return ids.DriveID{}, ErrAlreadyFormatted
	} else if !errors.Is(err, os.ErrNotExist) {
		return ids.DriveID{}, fmt.Errorf("local: check format: %w", err)
	}
	for _, dir := range append(lay.dirs(), filepath.Dir(lay.index)) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return ids.DriveID{}, fmt.Errorf("local: create %s: %w", dir, err)
		}
	}
	id := ids.NewDriveID()
	data, err := proto.Marshal(&uniov1.DriveFormat{
		FormatVersion:   formatVersion,
		ClusterId:       clusterID,
		DriveId:         id[:],
		CreatedUnixNano: time.Now().UnixNano(),
	})
	if err != nil {
		return ids.DriveID{}, fmt.Errorf("local: encode format: %w", err)
	}
	if err := writeFileAtomic(lay.format, data); err != nil {
		return ids.DriveID{}, err
	}
	return id, nil
}

// readFormat loads the drive id from the format record. A non-empty
// clusterID must match the record.
func readFormat(lay layout, clusterID string) (ids.DriveID, error) {
	data, err := os.ReadFile(lay.format)
	if errors.Is(err, os.ErrNotExist) {
		return ids.DriveID{}, ErrNotFormatted
	}
	if err != nil {
		return ids.DriveID{}, fmt.Errorf("local: read format: %w", err)
	}
	var rec uniov1.DriveFormat
	if err := proto.Unmarshal(data, &rec); err != nil {
		return ids.DriveID{}, fmt.Errorf("local: decode format: %w", err)
	}
	if rec.FormatVersion != formatVersion {
		return ids.DriveID{}, fmt.Errorf("local: unsupported format version %d", rec.FormatVersion)
	}
	if len(rec.DriveId) != len(ids.DriveID{}) {
		return ids.DriveID{}, errors.New("local: format record has a malformed drive id")
	}
	if clusterID != "" && rec.ClusterId != clusterID {
		return ids.DriveID{}, ErrClusterMismatch
	}
	var id ids.DriveID
	copy(id[:], rec.DriveId)
	return id, nil
}

// writeFileAtomic replaces path with data. It writes a temporary file, syncs
// it, renames it over path and syncs the directory.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
