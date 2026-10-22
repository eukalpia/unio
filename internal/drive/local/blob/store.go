// Package blob stores large shards as one file each (architecture section 8,
// ADR 0005). A blob is written to tmp/, fsynced, renamed into
// blobs/<xx>/<name> and the directory is fsynced. Names are the hex encoding
// of the binary ShardID, so they are never derived from user input.
package blob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/drive/local/internal/syncerr"
	"github.com/eukalpia/unio/internal/ids"
)

// directAlign is the alignment and length multiple required for O_DIRECT
// writes, and the block size of the blob writer.
const directAlign = 4096

// nameLen is the length of a shard file name in hex characters.
const nameLen = 2 * ids.ShardIDSize

// Store manages the blob files of one drive.
type Store struct {
	blobs string
	tmp   string
	pool  *bufpool.Pool
}

// NewStore returns a store that keeps blobs under blobsDir and stages writes
// in tmpDir. The pool supplies write buffers. Its size must be a multiple of
// 4096 and it must be aligned to 4096.
func NewStore(blobsDir, tmpDir string, pool *bufpool.Pool) (*Store, error) {
	if pool.Size()%directAlign != 0 {
		return nil, fmt.Errorf("blob: buffer size %d is not a multiple of %d", pool.Size(), directAlign)
	}
	for _, dir := range []string{blobsDir, tmpDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("blob: create %s: %w", dir, err)
		}
	}
	return &Store{blobs: blobsDir, tmp: tmpDir, pool: pool}, nil
}

// ShardName returns the file name of id: the hex encoding of its binary form.
func ShardName(id ids.ShardID) string {
	return hex.EncodeToString(id.AppendBinary(nil))
}

// ParseShardName is the inverse of ShardName. It rejects anything that is not
// exactly a shard name.
func ParseShardName(name string) (ids.ShardID, error) {
	if len(name) != nameLen {
		return ids.ShardID{}, fmt.Errorf("blob: name %q has length %d", name, len(name))
	}
	raw, err := hex.DecodeString(name)
	if err != nil {
		return ids.ShardID{}, fmt.Errorf("blob: name %q: %w", name, err)
	}
	return ids.ParseShardIDBinary(raw)
}

// Path returns the final path of the blob for id.
func (s *Store) Path(id ids.ShardID) string {
	name := ShardName(id)
	return filepath.Join(s.blobs, name[:2], name)
}

// NewTemp returns a fresh, unused path in the staging directory.
func (s *Store) NewTemp() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("blob: random name: %w", err)
	}
	return filepath.Join(s.tmp, hex.EncodeToString(b[:])+".tmp"), nil
}

// Write creates tmpPath and writes exactly size bytes from r into it, then
// makes the data durable. On any error the partial file is removed.
func (s *Store) Write(ctx context.Context, tmpPath string, r io.Reader, size int64) error {
	if err := writeFile(ctx, s.pool, tmpPath, r, size); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

// Place renames a written temporary file to its final path and makes the
// rename durable. The caller must ensure that final does not already hold a
// different shard.
func (s *Store) Place(tmpPath, final string) error {
	dir := filepath.Dir(final)
	created := false
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		created = true
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("blob: create %s: %w", dir, err)
		}
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return fmt.Errorf("blob: place %s: %w", final, err)
	}
	if err := syncDir(dir); err != nil {
		return syncerr.Wrap("fsync dir", dir, err)
	}
	if created {
		if err := syncDir(s.blobs); err != nil {
			return syncerr.Wrap("fsync dir", s.blobs, err)
		}
	}
	return nil
}

// Remove deletes a blob file and makes the removal durable. It returns an
// error satisfying errors.Is(err, os.ErrNotExist) for a missing file.
func (s *Store) Remove(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("blob: remove %s: %w", path, err)
	}
	if err := syncDir(filepath.Dir(path)); err != nil {
		return syncerr.Wrap("fsync dir", filepath.Dir(path), err)
	}
	return nil
}

// Walk calls fn for every blob file, in no particular order. Entries whose
// names are not shard names are skipped.
func (s *Store) Walk(fn func(id ids.ShardID, path string, size int64, modTime time.Time) error) error {
	dirs, err := os.ReadDir(s.blobs)
	if err != nil {
		return fmt.Errorf("blob: list %s: %w", s.blobs, err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(s.blobs, d.Name()))
		if err != nil {
			return fmt.Errorf("blob: list %s: %w", d.Name(), err)
		}
		for _, f := range files {
			id, err := ParseShardName(f.Name())
			if err != nil {
				continue
			}
			info, err := f.Info()
			if err != nil {
				return fmt.Errorf("blob: stat %s: %w", f.Name(), err)
			}
			if err := fn(id, filepath.Join(s.blobs, d.Name(), f.Name()), info.Size(), info.ModTime()); err != nil {
				return err
			}
		}
	}
	return nil
}

// CleanTemp removes every file in the staging directory. It runs at open,
// before any write can be in progress.
func (s *Store) CleanTemp() error {
	entries, err := os.ReadDir(s.tmp)
	if err != nil {
		return fmt.Errorf("blob: list %s: %w", s.tmp, err)
	}
	for _, e := range entries {
		if err := os.Remove(filepath.Join(s.tmp, e.Name())); err != nil {
			return fmt.Errorf("blob: remove temp %s: %w", e.Name(), err)
		}
	}
	return nil
}
