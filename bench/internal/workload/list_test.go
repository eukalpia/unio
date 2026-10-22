package workload

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/eukalpia/unio/bench/internal/fakestore"
	"github.com/eukalpia/unio/bench/internal/keys"
	"github.com/eukalpia/unio/bench/internal/objstore"
)

// expectedEntries counts the entries (objects, plus one per delimiter group)
// that a listing of prefix returns, computed directly from the layout.
func expectedEntries(t *testing.T, l keys.ListLayout, prefix, delimiter string) int {
	t.Helper()
	seen := make(map[string]bool)
	n := 0
	for i := 0; i < l.Keys; i++ {
		k := l.Key(i)
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		rest := k[len(prefix):]
		if delimiter != "" {
			if idx := strings.Index(rest, delimiter); idx >= 0 {
				group := rest[:idx+len(delimiter)]
				if !seen[group] {
					seen[group] = true
					n++
				}
				continue
			}
		}
		n++
	}
	return n
}

func newListSpec(t *testing.T, store *fakestore.Store, listKeys, page int) Spec {
	t.Helper()
	env := testEnv(store)
	env.ListKeys = listKeys
	env.ListPage = page
	spec, err := Build("list", env)
	if err != nil {
		t.Fatalf("Build(list): %v", err)
	}
	return spec
}

func TestListSpecShape(t *testing.T) {
	spec := newListSpec(t, fakestore.New(), 100, 10)
	if len(spec.Ops) != 2 || spec.Ops[0].Name != "LIST_DELIMITER" || spec.Ops[1].Name != "LIST_NO_DELIMITER" {
		t.Fatalf("ops = %+v, want LIST_DELIMITER and LIST_NO_DELIMITER", spec.Ops)
	}
	if spec.Setup == nil {
		t.Fatal("list workload has no prefill setup")
	}
}

func TestListSetupWritesEveryObject(t *testing.T) {
	const total = 2500
	store := fakestore.New()
	spec := newListSpec(t, store, total, 100)
	if err := spec.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if got := store.Objects(); got != total {
		t.Fatalf("objects = %d, want %d", got, total)
	}
	layout, err := keys.NewListLayout(total)
	if err != nil {
		t.Fatalf("NewListLayout: %v", err)
	}
	for i := 0; i < total; i++ {
		if _, ok := store.Size(layout.Key(i)); !ok {
			t.Fatalf("object %d (%q) was not written", i, layout.Key(i))
		}
	}
}

func TestListWorkerPaginatesWithContinuationTokens(t *testing.T) {
	const total, page, calls = 2500, 100, 60
	layout, err := keys.NewListLayout(total)
	if err != nil {
		t.Fatalf("NewListLayout: %v", err)
	}
	tests := []struct {
		name      string
		op        int
		delimiter string
	}{
		{name: "delimiter variant", op: 0, delimiter: "/"},
		{name: "no-delimiter variant", op: 1, delimiter: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := fakestore.New()
			spec := newListSpec(t, store, total, page)
			if err := spec.Setup(context.Background()); err != nil {
				t.Fatalf("Setup: %v", err)
			}
			w := spec.NewWorker(0)
			rng := rand.New(rand.NewPCG(3, 3))
			for i := 0; i < calls; i++ {
				if _, err := w.Do(context.Background(), tc.op, rng); err != nil {
					t.Fatalf("Do #%d: %v", i, err)
				}
			}

			traversals := splitTraversals(store.Lists())
			if len(traversals) < 2 {
				t.Fatalf("got %d complete traversals, want at least 2", len(traversals))
			}
			for ti, tr := range traversals {
				prefix := tr[0].Request.Prefix
				want := expectedEntries(t, layout, prefix, tc.delimiter)
				got := 0
				for pi, e := range tr {
					if e.Request.Delimiter != tc.delimiter {
						t.Fatalf("traversal %d page %d delimiter = %q, want %q", ti, pi, e.Request.Delimiter, tc.delimiter)
					}
					if e.Request.Prefix != prefix {
						t.Fatalf("traversal %d changed prefix mid-listing", ti)
					}
					if pi > 0 && e.Request.ContinuationToken != tr[pi-1].Page.NextToken {
						t.Fatalf("traversal %d page %d did not resume from the previous token", ti, pi)
					}
					entries := e.Page.Keys + e.Page.CommonPrefixes
					if pi < len(tr)-1 && entries != page {
						t.Fatalf("traversal %d page %d has %d entries, want a full page of %d", ti, pi, entries, page)
					}
					got += entries
				}
				if got != want {
					t.Fatalf("traversal %d of %q returned %d entries, want %d", ti, prefix, got, want)
				}
			}
		})
	}
}

// splitTraversals groups recorded pages into complete listings. A listing
// starts at a page with no continuation token and ends at the first page that
// is not truncated; an unfinished trailing listing is dropped.
func splitTraversals(log []fakestore.ListEntry) [][]fakestore.ListEntry {
	var out [][]fakestore.ListEntry
	var cur []fakestore.ListEntry
	for _, e := range log {
		if e.Request.ContinuationToken == "" {
			cur = nil
		}
		cur = append(cur, e)
		if !e.Page.Truncated {
			out = append(out, cur)
			cur = nil
		}
	}
	return out
}

func TestListWorkerResetsCursorAfterError(t *testing.T) {
	store := fakestore.New()
	spec := newListSpec(t, store, 2500, 100)
	if err := spec.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	errInjected := errors.New("injected list failure")
	var lists int
	store.Fail = func(call, _ string) error {
		if call != fakestore.CallList {
			return nil
		}
		lists++
		if lists == 2 {
			return errInjected
		}
		return nil
	}
	w := spec.NewWorker(0)
	rng := rand.New(rand.NewPCG(5, 5))
	ctx := context.Background()

	if _, err := w.Do(ctx, 1, rng); err != nil {
		t.Fatalf("first page: %v", err)
	}
	if _, err := w.Do(ctx, 1, rng); !errors.Is(err, errInjected) {
		t.Fatalf("second page error = %v, want the injected failure", err)
	}
	if _, err := w.Do(ctx, 1, rng); err != nil {
		t.Fatalf("third call: %v", err)
	}
	logged := store.Lists()
	if len(logged) != 2 {
		t.Fatalf("recorded %d pages, want 2", len(logged))
	}
	if tok := logged[1].Request.ContinuationToken; tok != "" {
		t.Fatalf("call after an error resumed with token %q; the cursor must restart", tok)
	}
}

func TestListWorkerEmptyPageIsError(t *testing.T) {
	store := fakestore.New()
	spec := newListSpec(t, store, 100, 10)
	// Setup is deliberately skipped, so every listing comes back empty.
	w := spec.NewWorker(0)
	if _, err := w.Do(context.Background(), 0, rand.New(rand.NewPCG(1, 1))); err == nil {
		t.Fatal("empty listing was accepted")
	}
}

// badListStore reports a truncated page without a continuation token.
type badListStore struct {
	*fakestore.Store
}

func (badListStore) ListObjectsV2(context.Context, string, objstore.ListRequest) (objstore.ListPage, error) {
	return objstore.ListPage{Keys: 1, Truncated: true}, nil
}

func TestListWorkerRejectsTruncatedPageWithoutToken(t *testing.T) {
	layout, err := keys.NewListLayout(100)
	if err != nil {
		t.Fatalf("NewListLayout: %v", err)
	}
	w := &listWorker{
		store:   badListStore{fakestore.New()},
		bucket:  "bench",
		layout:  layout,
		page:    10,
		cursors: make([]listCursor, 2),
	}
	if _, err := w.Do(context.Background(), 0, rand.New(rand.NewPCG(1, 1))); err == nil {
		t.Fatal("truncated page without a continuation token was accepted")
	}
}

func TestListRunReportsPagesAsSamples(t *testing.T) {
	store := fakestore.New()
	spec := newListSpec(t, store, 500, 100)
	res, err := Run(context.Background(), spec, RunConfig{Concurrency: 2, Duration: time.Hour, Seed: 1, MaxOpsPerWorker: 20})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	total := findOp(t, res, "TOTAL").Summary
	if total.Operations != 40 || total.Errors != 0 || total.Bytes != 0 {
		t.Fatalf("total = ops %d errors %d bytes %d, want 40 0 0", total.Operations, total.Errors, total.Bytes)
	}
	if store.Count(fakestore.CallList) != 40 {
		t.Fatalf("LIST calls = %d, want 40", store.Count(fakestore.CallList))
	}
}
