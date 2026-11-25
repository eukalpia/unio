package mapstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/eukalpia/unio/internal/cluster"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := testMap()
	if err := Save(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(want, got, cmpopts.EquateEmpty()); d != "" {
		t.Fatalf("loaded map differs:\n%s", d)
	}
}

func TestSaveReplacesTheStoredMap(t *testing.T) {
	dir := t.TempDir()
	first := testMap()
	second := testMap()
	second.Version = 2
	if err := Save(dir, first); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, second); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 2 {
		t.Fatalf("version %d after replacing, want 2", got.Version)
	}
	assertOnlyMapFile(t, dir)
}

func TestLoadNotFound(t *testing.T) {
	cases := []struct {
		name string
		dir  func(t *testing.T) string
	}{
		{name: "empty directory", dir: func(t *testing.T) string { return t.TempDir() }},
		{name: "missing directory", dir: func(t *testing.T) string { return filepath.Join(t.TempDir(), "absent") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.dir(t))
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("error %v, want ErrNotFound", err)
			}
		})
	}
}

func TestSaveRejectsNilAndEmptyDir(t *testing.T) {
	if err := Save(t.TempDir(), nil); err == nil {
		t.Fatal("Save accepted a nil map")
	}
	if err := Save("", testMap()); err == nil {
		t.Fatal("Save accepted an empty directory")
	}
	if _, err := Load(""); err == nil {
		t.Fatal("Load accepted an empty directory")
	}
}

func TestSaveCreatesDirectoryWithPrivateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	dir := filepath.Join(t.TempDir(), "meta")
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode %o, want 0700", got)
	}
	fi, err := os.Stat(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode %o, want 0600", got)
	}
}

func TestSaveKeepsAnExistingDirectoryMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	dir := filepath.Join(t.TempDir(), "meta")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o755 {
		t.Fatalf("Save changed an existing directory to mode %o", got)
	}
}

func TestSaveRejectsMapTooLargeToLoad(t *testing.T) {
	dir := t.TempDir()
	huge := testMap()
	huge.Drives[0].Path = "/" + strings.Repeat("p", maxPayload)
	err := Save(dir, huge)
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("Save of an oversized map: error %v, want a size limit error", err)
	}
	assertNoFiles(t, dir)
}

func TestSaveFailureLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	// A directory where the map file belongs makes the final rename fail.
	if err := os.Mkdir(filepath.Join(dir, fileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, testMap()); err == nil {
		t.Fatal("Save succeeded over a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatalf("directory holds %v after a failed save, want only the blocking directory", names(entries))
	}
}

func TestStaleTempFileIsIgnoredAndRemoved(t *testing.T) {
	dir := t.TempDir()
	stray := filepath.Join(dir, tempPrefix+"4242")
	if err := os.WriteFile(stray, []byte("torn write from a crash"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Load ignores a stray temporary file: there is no map yet.
	if _, err := Load(dir); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load with only a stray temp file: error %v, want ErrNotFound", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatal("Load removed a file it should only read")
	}

	// Save removes it.
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stray temp file survived Save: %v", err)
	}
	assertOnlyMapFile(t, dir)
}

func TestStrayTempFileOfAnotherNameIsKept(t *testing.T) {
	dir := t.TempDir()
	other := filepath.Join(dir, "operator-notes.txt")
	if err := os.WriteFile(other, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("Save removed a file it did not create: %v", err)
	}
}

func TestLoadRejectsOversizedFileWithoutReadingIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fileName)
	// A sparse file one byte over the limit costs no disk space to create.
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxFileSize + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Load(dir)
	if !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("error %v, want a corruption error about the size limit", err)
	}
}

// TestLoadDetectsEveryByteFlipOnDisk repeats the byte-flip check through the
// file system, so that the read path is covered as well as the decoder.
func TestLoadDetectsEveryByteFlipOnDisk(t *testing.T) {
	dir := t.TempDir()
	if err := Save(dir, testMap()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, fileName)
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range valid {
		b := append([]byte(nil), valid...)
		b[i] ^= 0x10
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(dir); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("byte %d flipped: error %v, want ErrCorrupt", i, err)
		}
	}
	if err := os.WriteFile(path, valid[:len(valid)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("half-written file: error %v, want ErrCorrupt", err)
	}
}

func TestLoadRejectsAZeroLengthFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("zero-length file: error %v, want ErrCorrupt", err)
	}
}

// assertOnlyMapFile fails unless dir holds the stored map and nothing else.
func assertOnlyMapFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		t.Fatalf("directory holds %v, want only %s", names(entries), fileName)
	}
}

// assertNoFiles fails unless dir is empty.
func assertNoFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("directory holds %v, want nothing", names(entries))
	}
}

// names lists directory entries for a failure message.
func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// readStored returns the stored map file bytes in dir.
func readStored(t *testing.T, dir string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// writeStored replaces the stored map file in dir with b.
func writeStored(t *testing.T, dir string, b []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// sameBytes reports whether two maps encode identically.
func sameBytes(t *testing.T, a, b *cluster.Map) bool {
	t.Helper()
	return bytes.Equal(encodedMap(t, a), encodedMap(t, b))
}
