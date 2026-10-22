// Package fakestore provides an in-memory objstore.Store for unit tests. It
// never touches the network, is safe for concurrent use, and records every
// call so tests can check what a workload issued. The CLI does not use it.
package fakestore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/eukalpia/unio/bench/internal/objstore"
)

// Call names recorded by the fake store.
const (
	CallBucket = "BUCKET"
	CallPut    = "PUT"
	CallGet    = "GET"
	CallList   = "LIST"
)

// defaultMaxKeys is the page size used when a request leaves MaxKeys at zero,
// matching the S3 default.
const defaultMaxKeys = 1000

// Hook observes or fails a call. A non-nil error returned from a Fail hook
// fails the call. Hooks run without the store lock held, so they may call
// back into the store.
type Hook func(call, key string) error

// ListEntry is one recorded ListObjectsV2 call and the page it returned.
type ListEntry struct {
	Request objstore.ListRequest
	Page    objstore.ListPage
}

// Store is an in-memory objstore.Store. Objects keep only their size, which is
// all the benchmark observes.
type Store struct {
	mu      sync.Mutex
	objects map[string]int64
	counts  map[string]int64
	lists   []ListEntry

	// Fail, when set, is consulted before each call. An error it returns is
	// the call's result.
	Fail Hook
	// After, when set, runs after each call completes, for example to cancel
	// a run at a precise point.
	After Hook
}

// New returns an empty Store.
func New() *Store {
	return &Store{objects: make(map[string]int64), counts: make(map[string]int64)}
}

// Count returns how many calls of the given kind were made.
func (s *Store) Count(call string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[call]
}

// Objects returns the number of stored objects.
func (s *Store) Objects() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.objects)
}

// Size returns the stored size of key and whether it exists.
func (s *Store) Size(key string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.objects[key]
	return n, ok
}

// Lists returns a copy of the recorded ListObjectsV2 calls, in call order.
func (s *Store) Lists() []ListEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.lists)
}

// EnsureBucket records the call and succeeds.
func (s *Store) EnsureBucket(ctx context.Context, _ string) error {
	return s.begin(ctx, CallBucket, "")
}

// PutObject stores the size of body under key.
func (s *Store) PutObject(ctx context.Context, _, key string, body []byte) error {
	if err := s.begin(ctx, CallPut, key); err != nil {
		return err
	}
	s.mu.Lock()
	s.objects[key] = int64(len(body))
	s.mu.Unlock()
	return s.end(ctx, CallPut, key)
}

// GetObject returns the stored size of key, or an error when it is missing.
func (s *Store) GetObject(ctx context.Context, _, key string) (int64, error) {
	if err := s.begin(ctx, CallGet, key); err != nil {
		return 0, err
	}
	s.mu.Lock()
	n, ok := s.objects[key]
	s.mu.Unlock()
	if !ok {
		return 0, fmt.Errorf("fakestore: no such key %q", key)
	}
	if err := s.end(ctx, CallGet, key); err != nil {
		return 0, err
	}
	return n, nil
}

// ListObjectsV2 pages through the stored keys in lexical order. It supports the
// Prefix, Delimiter, MaxKeys and ContinuationToken parameters with S3
// semantics: a common prefix counts as one entry, and a continuation token
// resumes after the last entry of the previous page.
func (s *Store) ListObjectsV2(ctx context.Context, _ string, req objstore.ListRequest) (objstore.ListPage, error) {
	if err := s.begin(ctx, CallList, req.Prefix); err != nil {
		return objstore.ListPage{}, err
	}
	s.mu.Lock()
	keys := make([]string, 0, len(s.objects))
	for k := range s.objects {
		if strings.HasPrefix(k, req.Prefix) {
			keys = append(keys, k)
		}
	}
	s.mu.Unlock()
	slices.Sort(keys)

	page := paginate(keys, req)
	s.mu.Lock()
	s.lists = append(s.lists, ListEntry{Request: req, Page: page})
	s.mu.Unlock()
	if err := s.end(ctx, CallList, req.Prefix); err != nil {
		return objstore.ListPage{}, err
	}
	return page, nil
}

// paginate builds one page from sorted keys that already match the prefix.
func paginate(keys []string, req objstore.ListRequest) objstore.ListPage {
	limit := int(req.MaxKeys)
	if limit <= 0 {
		limit = defaultMaxKeys
	}
	var page objstore.ListPage
	entries := 0
	var marker, lastCommon string
	for _, k := range keys {
		if skipped(k, req) {
			continue
		}
		entryKey, common := k, false
		if req.Delimiter != "" {
			rest := strings.TrimPrefix(k, req.Prefix)
			if i := strings.Index(rest, req.Delimiter); i >= 0 {
				entryKey = req.Prefix + rest[:i+len(req.Delimiter)]
				common = true
				if entryKey == lastCommon {
					continue
				}
				lastCommon = entryKey
			}
		}
		if entries == limit {
			page.Truncated = true
			page.NextToken = marker
			break
		}
		if common {
			page.CommonPrefixes++
		} else {
			page.Keys++
		}
		entries++
		marker = entryKey
	}
	return page
}

// skipped reports whether key k lies at or before the continuation token. A
// token that is a common prefix skips every key beneath it.
func skipped(k string, req objstore.ListRequest) bool {
	t := req.ContinuationToken
	if t == "" {
		return false
	}
	if k <= t {
		return true
	}
	return req.Delimiter != "" && strings.HasSuffix(t, req.Delimiter) && strings.HasPrefix(k, t)
}

// begin counts the call and consults the Fail hook.
func (s *Store) begin(ctx context.Context, call, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.counts[call]++
	s.mu.Unlock()
	if s.Fail != nil {
		return s.Fail(call, key)
	}
	return nil
}

// end runs the After hook once the call has taken effect.
func (s *Store) end(_ context.Context, call, key string) error {
	if s.After != nil {
		return s.After(call, key)
	}
	return nil
}

var _ objstore.Store = (*Store)(nil)

// Keys returns the stored keys in lexical order.
func (s *Store) Keys() []string {
	s.mu.Lock()
	keys := make([]string, 0, len(s.objects))
	for k := range s.objects {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	slices.Sort(keys)
	return keys
}
