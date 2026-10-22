package metatest

import (
	"context"
	"sort"
	"strings"

	"github.com/eukalpia/unio/internal/meta"
)

// checkRevision applies the SystemKV condition: expect is AnyRevision, or it
// equals the current revision (0 for a missing key).
func checkRevision(expect, current uint64) error {
	if expect == meta.AnyRevision || expect == current {
		return nil
	}
	return meta.ErrRevisionMismatch
}

// Get implements meta.SystemKV.
func (f *Fake) Get(_ context.Context, key string) ([]byte, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.kv[key]
	if !ok {
		return nil, 0, meta.ErrNotFound
	}
	return cloneBytes(e.value), e.revision, nil
}

// Put implements meta.SystemKV. Every successful Put takes a revision greater
// than any revision handed out before.
func (f *Fake) Put(_ context.Context, key string, value []byte, expect uint64) (uint64, error) {
	if len(value) > meta.MaxSystemValueSize {
		return 0, meta.ErrValueTooLarge
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := checkRevision(expect, f.kv[key].revision); err != nil {
		return 0, err
	}
	f.revision++
	f.kv[key] = kvEntry{value: cloneBytes(value), revision: f.revision}
	return f.revision, nil
}

// Delete implements meta.SystemKV. Deleting a missing key succeeds when the
// condition matches a missing key, which has revision 0.
func (f *Fake) Delete(_ context.Context, key string, expect uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := checkRevision(expect, f.kv[key].revision); err != nil {
		return err
	}
	delete(f.kv, key)
	return nil
}

// List implements meta.SystemKV. Entries are ordered by key.
func (f *Fake) List(_ context.Context, prefix string) ([]meta.KV, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(f.kv))
	for k := range f.kv {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]meta.KV, 0, len(keys))
	for _, k := range keys {
		e := f.kv[k]
		out = append(out, meta.KV{Key: k, Value: cloneBytes(e.value), Revision: e.revision})
	}
	return out, nil
}
