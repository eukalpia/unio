// Package metatest provides an in-memory reference implementation of
// meta.Store and meta.SystemKV, in-process cluster fakes for the Raft-backed
// store, and the conformance suites that every implementation must pass.
//
// The Fake is the reference for the semantics documented on the meta
// interfaces. Conformance suites are exported as RunStoreSuite and
// RunSystemKVSuite and must not depend on the Fake's internals.
package metatest

import (
	"sort"
	"strings"
	"sync"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

var (
	_ meta.Store    = (*Fake)(nil)
	_ meta.SystemKV = (*Fake)(nil)
)

// Fake is an in-memory meta.Store and meta.SystemKV. All state is guarded by
// one mutex, so every method is atomic with respect to every other. Records
// passed in are copied on the way in and copied on the way out, so callers
// never alias stored state. The zero value is not usable; use NewFake.
type Fake struct {
	mu sync.Mutex
	// lastBucketID is the last BucketID handed out; IDs are never reused.
	lastBucketID ids.BucketID
	buckets      map[string]*bucketState
	// revision is the last SystemKV revision handed out.
	revision uint64
	kv       map[string]kvEntry
}

// NewFake returns an empty Fake with no buckets and no SystemKV entries.
func NewFake() *Fake {
	return &Fake{
		buckets: make(map[string]*bucketState),
		kv:      make(map[string]kvEntry),
	}
}

// bucketState is the state of one bucket.
type bucketState struct {
	info object.BucketInfo
	// objects maps a key to its versions in write order. The latest version
	// is the last element. Keys without versions are absent.
	objects map[string][]meta.ObjectRecord
	// uploads maps an upload id to its in-progress upload. Upload ids are
	// unique within a bucket.
	uploads map[string]*uploadState
}

// uploadState is one in-progress multipart upload.
type uploadState struct {
	rec   meta.UploadRecord
	parts map[int]meta.PartRecord
}

// kvEntry is one SystemKV entry.
type kvEntry struct {
	value    []byte
	revision uint64
}

// newBucketState returns the empty state of a bucket described by info.
func newBucketState(info object.BucketInfo) *bucketState {
	return &bucketState{
		info:    info,
		objects: make(map[string][]meta.ObjectRecord),
		uploads: make(map[string]*uploadState),
	}
}

// bucketLocked returns the named bucket or NoSuchBucket. f.mu must be held.
func (f *Fake) bucketLocked(name string) (*bucketState, error) {
	b, ok := f.buckets[name]
	if !ok {
		return nil, s3err.Errorf(s3err.NoSuchBucket, "bucket %q does not exist", name)
	}
	return b, nil
}

// latestVersion returns the newest version of a key, which may be a delete
// marker. ok is false when the key has no versions.
func latestVersion(vs []meta.ObjectRecord) (rec meta.ObjectRecord, ok bool) {
	if len(vs) == 0 {
		return meta.ObjectRecord{}, false
	}
	return vs[len(vs)-1], true
}

// versionIndex returns the index of versionID in vs, or -1.
func versionIndex(vs []meta.ObjectRecord, versionID string) int {
	for i := range vs {
		if vs[i].VersionID == versionID {
			return i
		}
	}
	return -1
}

// removeAt returns vs without element i. It never modifies vs.
func removeAt(vs []meta.ObjectRecord, i int) []meta.ObjectRecord {
	out := make([]meta.ObjectRecord, 0, len(vs)-1)
	out = append(out, vs[:i]...)
	return append(out, vs[i+1:]...)
}

// writeCondition evaluates cond against the latest version of a key, which
// is the precondition check of every write. Delete markers count as absent.
func writeCondition(vs []meta.ObjectRecord, cond object.Conditions) error {
	latest, ok := latestVersion(vs)
	exists := ok && !latest.DeleteMarker
	return cond.EvaluateWrite(exists, latest.ETag)
}

// validVersioning reports whether state is a known versioning state.
func validVersioning(state object.VersioningState) error {
	switch state {
	case object.VersioningUnversioned, object.VersioningEnabled, object.VersioningSuspended:
		return nil
	}
	return s3err.Errorf(s3err.InvalidArgument, "unknown versioning state %d", state)
}

// pageSize resolves a requested page size: 0 means object.MaxListKeys, and
// negative or larger values are InvalidArgument.
func pageSize(n int) (int, error) {
	switch {
	case n == 0:
		return object.MaxListKeys, nil
	case n < 0 || n > object.MaxListKeys:
		return 0, s3err.Errorf(s3err.InvalidArgument, "page size %d is outside 1..%d", n, object.MaxListKeys)
	}
	return n, nil
}

// rollup maps a name to its entry in a delimited listing. When delimiter is
// non-empty and the part of name after prefix contains it, the entry is the
// common prefix up to and including the first delimiter, and isPrefix is
// true. Otherwise the entry is name itself.
func rollup(name, prefix, delimiter string) (entry string, isPrefix bool) {
	if delimiter == "" {
		return name, false
	}
	rem := name[len(prefix):]
	i := strings.Index(rem, delimiter)
	if i < 0 {
		return name, false
	}
	return prefix + rem[:i+len(delimiter)], true
}

// sortedKeys returns the keys of m in byte order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
