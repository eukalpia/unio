package metatest

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
)

// kvCases returns every meta.SystemKV case in run order.
func kvCases() []kvCase {
	return []kvCase{
		{"KV/GetMissing", caseKVGetMissing},
		{"KV/PutCreateAndGet", caseKVPutCreateAndGet},
		{"KV/PutRequiresMatchingRevision", caseKVPutRevisionRules},
		{"KV/RevisionsStrictlyIncrease", caseKVRevisionsIncrease},
		{"KV/AnyRevision", caseKVAnyRevision},
		{"KV/DeleteRevisionRules", caseKVDeleteRevisionRules},
		{"KV/RecreateAfterDeleteGetsNewRevision", caseKVRecreateAfterDelete},
		{"KV/ValueSizeLimit", caseKVValueSizeLimit},
		{"KV/ValuesAreCopies", caseKVValuesAreCopies},
		{"KV/EmptyValue", caseKVEmptyValue},
		{"KV/ListPrefixOrder", caseKVListPrefixOrder},
		{"KV/ConcurrentCompareAndSwap", caseKVConcurrentCAS},
	}
}

func caseKVGetMissing(t *testing.T, kv meta.SystemKV) {
	_, _, err := kv.Get(t.Context(), "absent")
	requireSentinel(t, err, meta.ErrNotFound)
}

func caseKVPutCreateAndGet(t *testing.T, kv meta.SystemKV) {
	rev := mustPut(t, kv, "iam/root", "v1", 0)
	if rev == 0 {
		t.Fatal("first Put returned revision 0")
	}
	value, got, err := kv.Get(t.Context(), "iam/root")
	mustNoErr(t, err)
	if string(value) != "v1" || got != rev {
		t.Fatalf("Get = (%q, %d), want (%q, %d)", value, got, "v1", rev)
	}
}

// caseKVPutRevisionRules checks the compare-and-swap rules of Put: a create
// needs revision 0, an update needs the current revision.
func caseKVPutRevisionRules(t *testing.T, kv meta.SystemKV) {
	rev := mustPut(t, kv, "k", "v1", 0)

	_, err := kv.Put(t.Context(), "k", []byte("v2"), 0)
	requireSentinel(t, err, meta.ErrRevisionMismatch)
	_, err = kv.Put(t.Context(), "k", []byte("v2"), rev+100)
	requireSentinel(t, err, meta.ErrRevisionMismatch)

	next, err := kv.Put(t.Context(), "k", []byte("v2"), rev)
	mustNoErr(t, err)
	if next <= rev {
		t.Fatalf("update revision %d is not greater than %d", next, rev)
	}
	value, got, err := kv.Get(t.Context(), "k")
	mustNoErr(t, err)
	if string(value) != "v2" || got != next {
		t.Fatalf("Get = (%q, %d), want (%q, %d)", value, got, "v2", next)
	}

	// The old revision is now stale.
	_, err = kv.Put(t.Context(), "k", []byte("v3"), rev)
	requireSentinel(t, err, meta.ErrRevisionMismatch)
}

// caseKVRevisionsIncrease checks that every successful Put returns a revision
// greater than any revision returned before, across keys.
func caseKVRevisionsIncrease(t *testing.T, kv meta.SystemKV) {
	var last uint64
	for i := range 20 {
		key := fmt.Sprintf("key-%d", i%3)
		var expect uint64
		if _, rev, err := kv.Get(t.Context(), key); err == nil {
			expect = rev
		}
		rev := mustPut(t, kv, key, fmt.Sprintf("value %d", i), expect)
		if rev <= last {
			t.Fatalf("put %d returned revision %d, not greater than %d", i, rev, last)
		}
		last = rev
	}
}

func caseKVAnyRevision(t *testing.T, kv meta.SystemKV) {
	_, err := kv.Put(t.Context(), "fresh", []byte("a"), meta.AnyRevision)
	mustNoErr(t, err)
	_, err = kv.Put(t.Context(), "fresh", []byte("b"), meta.AnyRevision)
	mustNoErr(t, err)
	value, _, err := kv.Get(t.Context(), "fresh")
	mustNoErr(t, err)
	if string(value) != "b" {
		t.Fatalf("value = %q, want b", value)
	}
	mustNoErr(t, kv.Delete(t.Context(), "fresh", meta.AnyRevision))
	_, _, err = kv.Get(t.Context(), "fresh")
	requireSentinel(t, err, meta.ErrNotFound)
	mustNoErr(t, kv.Delete(t.Context(), "never-existed", meta.AnyRevision))
}

func caseKVDeleteRevisionRules(t *testing.T, kv meta.SystemKV) {
	// Deleting a missing key succeeds only when the condition matches revision 0.
	mustNoErr(t, kv.Delete(t.Context(), "absent", 0))
	err := kv.Delete(t.Context(), "absent", 5)
	requireSentinel(t, err, meta.ErrRevisionMismatch)

	rev := mustPut(t, kv, "k", "v", 0)
	err = kv.Delete(t.Context(), "k", 0)
	requireSentinel(t, err, meta.ErrRevisionMismatch)
	err = kv.Delete(t.Context(), "k", rev+1)
	requireSentinel(t, err, meta.ErrRevisionMismatch)
	_, _, err = kv.Get(t.Context(), "k")
	mustNoErr(t, err)

	mustNoErr(t, kv.Delete(t.Context(), "k", rev))
	_, _, err = kv.Get(t.Context(), "k")
	requireSentinel(t, err, meta.ErrNotFound)
}

func caseKVRecreateAfterDelete(t *testing.T, kv meta.SystemKV) {
	rev := mustPut(t, kv, "k", "v1", 0)
	mustNoErr(t, kv.Delete(t.Context(), "k", rev))
	again := mustPut(t, kv, "k", "v2", 0)
	if again <= rev {
		t.Fatalf("recreated revision %d is not greater than deleted revision %d", again, rev)
	}
}

func caseKVValueSizeLimit(t *testing.T, kv meta.SystemKV) {
	mustPut(t, kv, "k", "small", 0)
	tooBig := make([]byte, meta.MaxSystemValueSize+1)
	_, err := kv.Put(t.Context(), "k", tooBig, meta.AnyRevision)
	requireSentinel(t, err, meta.ErrValueTooLarge)
	value, _, err := kv.Get(t.Context(), "k")
	mustNoErr(t, err)
	if string(value) != "small" {
		t.Fatal("a rejected oversize Put changed the value")
	}

	atLimit := make([]byte, meta.MaxSystemValueSize)
	_, err = kv.Put(t.Context(), "k", atLimit, meta.AnyRevision)
	mustNoErr(t, err)
	value, _, err = kv.Get(t.Context(), "k")
	mustNoErr(t, err)
	if len(value) != meta.MaxSystemValueSize {
		t.Fatalf("value length = %d, want %d", len(value), meta.MaxSystemValueSize)
	}
}

func caseKVValuesAreCopies(t *testing.T, kv meta.SystemKV) {
	in := []byte("original")
	_, err := kv.Put(t.Context(), "k", in, meta.AnyRevision)
	mustNoErr(t, err)
	in[0] = 'X'

	out, _, err := kv.Get(t.Context(), "k")
	mustNoErr(t, err)
	if string(out) != "original" {
		t.Fatalf("stored value changed through the input slice: %q", out)
	}
	out[0] = 'Y'
	again, _, err := kv.Get(t.Context(), "k")
	mustNoErr(t, err)
	if string(again) != "original" {
		t.Fatalf("stored value changed through the returned slice: %q", again)
	}
}

func caseKVEmptyValue(t *testing.T, kv meta.SystemKV) {
	mustPut(t, kv, "empty", "", 0)
	value, _, err := kv.Get(t.Context(), "empty")
	mustNoErr(t, err)
	if len(value) != 0 {
		t.Fatalf("empty value read back as %q", value)
	}
}

func caseKVListPrefixOrder(t *testing.T, kv meta.SystemKV) {
	revs := map[string]uint64{}
	for _, k := range []string{"iam/users/b", "iam/users/a", "iam/keys/z", "bucket/x"} {
		revs[k] = mustPut(t, kv, k, "value of "+k, 0)
	}

	entries, err := kv.List(t.Context(), "iam/")
	mustNoErr(t, err)
	wantKeys := []string{"iam/keys/z", "iam/users/a", "iam/users/b"}
	if len(entries) != len(wantKeys) {
		t.Fatalf("List(iam/) returned %d entries, want %d", len(entries), len(wantKeys))
	}
	for i, e := range entries {
		if e.Key != wantKeys[i] {
			t.Fatalf("entry %d key = %q, want %q", i, e.Key, wantKeys[i])
		}
		if e.Revision != revs[e.Key] || !bytes.Equal(e.Value, []byte("value of "+e.Key)) {
			t.Fatalf("entry %q = (%q, %d), want revision %d", e.Key, e.Value, e.Revision, revs[e.Key])
		}
	}

	all, err := kv.List(t.Context(), "")
	mustNoErr(t, err)
	if len(all) != 4 || all[0].Key != "bucket/x" || all[3].Key != "iam/users/b" {
		t.Fatalf("List(\"\") = %d entries, want all four in key order", len(all))
	}

	none, err := kv.List(t.Context(), "nothing/")
	mustNoErr(t, err)
	if len(none) != 0 {
		t.Fatalf("List(nothing/) returned %d entries", len(none))
	}
}

// caseKVConcurrentCAS races writers that all expect the key to be absent.
// Exactly one may succeed.
func caseKVConcurrentCAS(t *testing.T, kv meta.SystemKV) {
	const writers = 8
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := kv.Put(t.Context(), "election", []byte(fmt.Sprintf("writer %d", i)), 0)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, meta.ErrRevisionMismatch):
		default:
			t.Errorf("losing writer got %v, want ErrRevisionMismatch", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d writers succeeded with expect 0, want exactly 1", won)
	}
}
