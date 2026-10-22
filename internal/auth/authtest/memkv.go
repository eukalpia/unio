package authtest

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/eukalpia/unio/internal/meta"
)

// MemKV is an in-memory meta.SystemKV. It follows the store's contract:
// revisions are unique and increase with every write, and compare-and-swap
// writes fail with meta.ErrRevisionMismatch. It is safe for concurrent use.
type MemKV struct {
	mu   sync.Mutex
	rev  uint64
	data map[string]memEntry
}

type memEntry struct {
	value []byte
	rev   uint64
}

var _ meta.SystemKV = (*MemKV)(nil)

// NewMemKV returns an empty MemKV.
func NewMemKV() *MemKV {
	return &MemKV{data: make(map[string]memEntry)}
}

// Get implements meta.SystemKV. It returns a copy of the value.
func (m *MemKV) Get(ctx context.Context, key string) ([]byte, uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key]
	if !ok {
		return nil, 0, meta.ErrNotFound
	}
	return slices.Clone(e.value), e.rev, nil
}

// Put implements meta.SystemKV. The value is copied, so the caller may reuse
// its buffer.
func (m *MemKV) Put(ctx context.Context, key string, value []byte, expectRevision uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(value) > meta.MaxSystemValueSize {
		return 0, meta.ErrValueTooLarge
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, exists := m.data[key]
	if err := checkRevision(exists, cur.rev, expectRevision); err != nil {
		return 0, err
	}
	m.rev++
	m.data[key] = memEntry{value: slices.Clone(value), rev: m.rev}
	return m.rev, nil
}

// Delete implements meta.SystemKV. Deleting a missing key succeeds when no
// particular revision is expected.
func (m *MemKV) Delete(ctx context.Context, key string, expectRevision uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, exists := m.data[key]
	if err := checkRevision(exists, cur.rev, expectRevision); err != nil {
		return err
	}
	delete(m.data, key)
	return nil
}

// List implements meta.SystemKV. Entries come back sorted by key.
func (m *MemKV) List(ctx context.Context, prefix string) ([]meta.KV, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.data))
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := make([]meta.KV, 0, len(keys))
	for _, k := range keys {
		e := m.data[k]
		out = append(out, meta.KV{Key: k, Value: slices.Clone(e.value), Revision: e.rev})
	}
	return out, nil
}

// checkRevision applies the expected-revision rule: AnyRevision always
// passes, 0 requires the key to be absent, and any other value requires the
// key to exist at exactly that revision.
func checkRevision(exists bool, rev, expect uint64) error {
	switch expect {
	case meta.AnyRevision:
		return nil
	case 0:
		if exists {
			return meta.ErrRevisionMismatch
		}
		return nil
	}
	if !exists || rev != expect {
		return meta.ErrRevisionMismatch
	}
	return nil
}
