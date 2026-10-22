package local

import (
	"fmt"
	"os"
	"sync"

	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// segTable holds the open segment files of a drive. Readers take a reference
// to a segment, so compaction can retire it while a read is in progress: the
// file is removed when the last reader releases it.
type segTable struct {
	mu    sync.Mutex
	files map[uint64]*segRef
}

// segRef is an open segment with its reader count.
type segRef struct {
	file    *segment.File
	refs    int
	retired bool
	closed  bool
}

func newSegTable() *segTable {
	return &segTable{files: make(map[uint64]*segRef)}
}

// add registers an open segment.
func (t *segTable) add(id uint64, f *segment.File) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.files[id] = &segRef{file: f}
}

// acquire takes a reference to segment id. It reports false if the segment is
// unknown or retired.
func (t *segTable) acquire(id uint64) (*segRef, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ref, ok := t.files[id]
	if !ok || ref.retired {
		return nil, false
	}
	ref.refs++
	return ref, true
}

// release drops a reference taken by acquire. The last release of a retired
// segment closes and removes it.
func (t *segTable) release(ref *segRef) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	ref.refs--
	if ref.refs == 0 && ref.retired {
		return closeAndRemove(ref)
	}
	return nil
}

// retire removes segment id from the table. The file is removed now if no
// reader holds it, and otherwise by the last release.
func (t *segTable) retire(id uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	ref, ok := t.files[id]
	if !ok {
		return nil
	}
	delete(t.files, id)
	ref.retired = true
	if ref.refs == 0 {
		return closeAndRemove(ref)
	}
	return nil
}

// closeAll closes every segment file without removing any. It is used at
// Close, when readers can no longer be served anyway.
func (t *segTable) closeAll() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var first error
	for id, ref := range t.files {
		if err := closeOnce(ref); err != nil && first == nil {
			first = err
		}
		delete(t.files, id)
	}
	return first
}

func closeOnce(ref *segRef) error {
	if ref.closed {
		return nil
	}
	ref.closed = true
	return ref.file.Close()
}

func closeAndRemove(ref *segRef) error {
	if err := closeOnce(ref); err != nil {
		return err
	}
	if err := os.Remove(ref.file.Path()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("local: remove segment %s: %w", ref.file.Path(), err)
	}
	return nil
}
