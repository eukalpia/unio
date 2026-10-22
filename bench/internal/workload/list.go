package workload

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/eukalpia/unio/bench/internal/keys"
	"github.com/eukalpia/unio/bench/internal/objstore"
	"github.com/eukalpia/unio/bench/internal/payload"
)

// Operation indices of the LIST workload. Index 0 lists with Delimiter "/" and
// index 1 lists the same prefixes without a delimiter.
const (
	opListDelimiter = 0
	listOpCount     = 2
)

// listDelimiter is the delimiter used by the delimiter variant.
const listDelimiter = "/"

// listObjectSize is the body size of the pre-filled LIST objects. Small
// objects keep the pre-fill cheap so that very large listings are practical.
const listObjectSize = 128

// maxListPage is the largest MaxKeys that S3 accepts.
const maxListPage = 1000

// listCursor is one worker's position in a paginated listing. A listing starts
// with no token and ends at the first page that is not truncated.
type listCursor struct {
	active bool
	prefix int
	token  string
}

// listWorker pages through the LIST layout. Each worker keeps one cursor per
// operation type, so the two variants never share a continuation token.
type listWorker struct {
	store   objstore.Store
	bucket  string
	layout  keys.ListLayout
	page    int32
	cursors []listCursor
}

// Do fetches the next page of the current listing, or starts a new listing on a
// random prefix when the previous one has finished. Every page is one sample.
func (w *listWorker) Do(ctx context.Context, op int, rng *rand.Rand) (int64, error) {
	if op < 0 || op >= len(w.cursors) {
		return 0, fmt.Errorf("operation index %d out of range", op)
	}
	c := &w.cursors[op]
	if !c.active {
		c.active = true
		c.prefix = rng.IntN(w.layout.Prefixes)
		c.token = ""
	}
	req := objstore.ListRequest{
		Prefix:            w.layout.Prefix(c.prefix),
		MaxKeys:           w.page,
		ContinuationToken: c.token,
	}
	if op == opListDelimiter {
		req.Delimiter = listDelimiter
	}
	page, err := w.store.ListObjectsV2(ctx, w.bucket, req)
	if err != nil {
		c.active = false
		return 0, fmt.Errorf("list %q: %w", req.Prefix, err)
	}
	if page.Keys+page.CommonPrefixes == 0 {
		c.active = false
		return 0, fmt.Errorf("list %q: empty page for a prefix that was pre-filled", req.Prefix)
	}
	if !page.Truncated {
		c.active = false
		return 0, nil
	}
	if page.NextToken == "" {
		c.active = false
		return 0, fmt.Errorf("list %q: truncated page without a continuation token", req.Prefix)
	}
	c.token = page.NextToken
	return 0, nil
}

// listSpec builds the LIST workload. Setup writes env.ListKeys small objects,
// and the two operation types page through them.
func listSpec(env Env) (Spec, error) {
	if env.ListKeys < 1 {
		return Spec{}, fmt.Errorf("list needs at least 1 key, got %d", env.ListKeys)
	}
	if env.ListPage < 1 || env.ListPage > maxListPage {
		return Spec{}, fmt.Errorf("list page must be between 1 and %d, got %d", maxListPage, env.ListPage)
	}
	layout, err := keys.NewListLayout(env.ListKeys)
	if err != nil {
		return Spec{}, err
	}
	buf, err := payload.NewBuffer(listObjectSize, env.Seed)
	if err != nil {
		return Spec{}, err
	}
	page := int32(env.ListPage)
	return Spec{
		Name: "list",
		Ops: []OpSpec{
			{Name: "LIST_DELIMITER", Weight: 1},
			{Name: "LIST_NO_DELIMITER", Weight: 1},
		},
		Setup: func(ctx context.Context) error {
			return parallelFor(ctx, layout.Keys, env.Concurrency, func(ctx context.Context, i int) error {
				key := layout.Key(i)
				if err := env.Store.PutObject(ctx, env.Bucket, key, buf.Bytes()); err != nil {
					return fmt.Errorf("prefill %q: %w", key, err)
				}
				return nil
			})
		},
		NewWorker: func(int) Worker {
			return &listWorker{
				store:   env.Store,
				bucket:  env.Bucket,
				layout:  layout,
				page:    page,
				cursors: make([]listCursor, listOpCount),
			}
		},
	}, nil
}
