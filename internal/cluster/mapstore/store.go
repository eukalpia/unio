// Package mapstore keeps the cluster map on a node's metadata directory, so
// that the node can check the configured layout against the one the cluster
// was formed with before it touches any drive or Raft group.
//
// The map lives in cluster-map.pb. Save replaces the file atomically, Load
// verifies its checksum and size, and Reconcile decides between the two on
// startup. A node that disagrees with the stored map does not start.
package mapstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/mappb"
)

const (
	// fileName is the stored cluster map inside the metadata directory.
	fileName = "cluster-map.pb"

	// tempPrefix starts the names of the temporary files that Save writes
	// before it renames them over fileName. A crash can leave them behind.
	tempPrefix = fileName + ".tmp-"
)

// layoutHint ends the error that Reconcile returns on a mismatch. A
// cluster's layout is fixed once it is formed.
const layoutHint = "changing the cluster layout of an existing cluster is not supported in v0.1, so restore the configuration that formed it"

// ErrNotFound reports that the metadata directory holds no stored cluster map.
var ErrNotFound = errors.New("mapstore: no stored cluster map")

// ErrMismatch reports that the stored cluster map differs from the configured
// one. Reconcile wraps it and names the first difference.
var ErrMismatch = errors.New("mapstore: stored cluster map differs from the configured one")

// ErrCorrupt reports that the stored cluster map file is damaged: it has the
// wrong magic, an unknown format version, a bad checksum, a bad length or a
// payload that does not decode. Load wraps it, so callers test with errors.Is.
var ErrCorrupt = errors.New("mapstore: cluster map file is corrupt")

// Load reads and verifies the cluster map stored in dir. It returns an error
// wrapping ErrNotFound if dir holds no map, and one wrapping ErrCorrupt if the
// file fails any check. Load reads at most 16 MiB and never panics on file
// contents.
func Load(dir string) (*cluster.Map, error) {
	path, err := mapPath(dir)
	if err != nil {
		return nil, err
	}
	b, err := readFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%w in %s", ErrNotFound, dir)
	case err != nil:
		return nil, err
	}
	return decode(b)
}

// readFile returns the contents of path. It checks the size before reading,
// so an oversized file is never read into memory.
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path) // #nosec G304 -- path is the metadata directory joined with a fixed name
	if err != nil {
		return nil, fmt.Errorf("mapstore: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("mapstore: stat %s: %w", path, err)
	}
	if info.Size() > maxFileSize {
		return nil, corruptf("%s is %d bytes, the limit is %d", path, info.Size(), maxFileSize)
	}
	b := make([]byte, info.Size())
	if _, err := io.ReadFull(f, b); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, corruptf("%s shrank while it was read", path)
		}
		return nil, fmt.Errorf("mapstore: read %s: %w", path, err)
	}
	return b, nil
}

// Save stores m in dir, replacing any map already there. It creates dir with
// mode 0700 if it is missing, and the file has mode 0600. Save writes a
// temporary file in dir, syncs it, renames it over the stored map and syncs
// dir, so a crash leaves either the old map or the new one. A failed Save
// removes its temporary file.
func Save(dir string, m *cluster.Map) error {
	if m == nil {
		return errors.New("mapstore: nil cluster map")
	}
	data, err := encode(m)
	if err != nil {
		return err
	}
	if _, err := mapPath(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mapstore: create %s: %w", dir, err)
	}
	if err := removeStaleTemps(dir); err != nil {
		return err
	}
	return writeFileAtomic(dir, data)
}

// writeFileAtomic replaces the stored map in dir with data.
func writeFileAtomic(dir string, data []byte) error {
	f, err := os.CreateTemp(dir, tempPrefix+"*") // #nosec G304 -- dir is the metadata directory and the suffix is random
	if err != nil {
		return fmt.Errorf("mapstore: create temporary file in %s: %w", dir, err)
	}
	tmp := f.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmp)
		}
	}()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("mapstore: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, fileName)); err != nil {
		return fmt.Errorf("mapstore: replace %s: %w", fileName, err)
	}
	renamed = true
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("mapstore: sync %s: %w", dir, err)
	}
	return nil
}

// removeStaleTemps deletes the temporary files that an interrupted Save left
// in dir. It touches only names with tempPrefix. A missing dir has nothing to
// remove.
func removeStaleTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mapstore: list %s: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("mapstore: remove stale %s: %w", e.Name(), err)
		}
	}
	return nil
}

// Reconcile checks the configured cluster map want against the one stored in
// dir and returns the map the node must run with. If nothing is stored, it
// saves want and returns it. If the stored map encodes the same as want, it
// returns the stored map. Otherwise it returns an error wrapping ErrMismatch
// that names the first difference, and it changes nothing. A corrupt stored
// map is an error that wraps ErrCorrupt. Reconcile never overwrites it.
func Reconcile(dir string, want *cluster.Map) (*cluster.Map, error) {
	if want == nil {
		return nil, errors.New("mapstore: nil cluster map")
	}
	if _, err := mapPath(dir); err != nil {
		return nil, err
	}
	if err := removeStaleTemps(dir); err != nil {
		return nil, err
	}
	stored, err := Load(dir)
	if errors.Is(err, ErrNotFound) {
		if err := Save(dir, want); err != nil {
			return nil, err
		}
		return want, nil
	}
	if err != nil {
		return nil, err
	}
	storedBytes, err := mappb.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("mapstore: encode stored cluster map: %w", err)
	}
	wantBytes, err := mappb.Marshal(want)
	if err != nil {
		return nil, fmt.Errorf("mapstore: encode configured cluster map: %w", err)
	}
	if bytes.Equal(storedBytes, wantBytes) {
		return stored, nil
	}
	return nil, fmt.Errorf("%w: %s; hint: %s", ErrMismatch, describeDifference(stored, want), layoutHint)
}

// mapPath returns the path of the stored map in dir. The directory must be
// set: an empty dir would name a file in the working directory.
func mapPath(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("mapstore: metadata directory is required")
	}
	return filepath.Join(dir, fileName), nil
}
