package workload

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/eukalpia/unio/bench/internal/keys"
	"github.com/eukalpia/unio/bench/internal/objstore"
	"github.com/eukalpia/unio/bench/internal/payload"
)

// opKind is the request a single object operation issues.
type opKind int

const (
	kindPut opKind = iota + 1
	kindGet
)

// weightedKind couples an operation name, its weight and its request kind.
type weightedKind struct {
	name   string
	weight int
	kind   opKind
}

// objectWorker issues PUT and GET requests against one keyspace. Every request
// of one size shares the same payload buffer.
type objectWorker struct {
	store  objstore.Store
	bucket string
	space  keys.Keyspace
	buf    payload.Buffer
	kinds  []opKind
}

// Do performs operation op on a key drawn from the keyspace. A GET whose body
// is not exactly the payload size is an error.
func (w *objectWorker) Do(ctx context.Context, op int, rng *rand.Rand) (int64, error) {
	if op < 0 || op >= len(w.kinds) {
		return 0, fmt.Errorf("operation index %d out of range", op)
	}
	key := w.space.Pick(rng)
	switch w.kinds[op] {
	case kindPut:
		if err := w.store.PutObject(ctx, w.bucket, key, w.buf.Bytes()); err != nil {
			return 0, err
		}
		return int64(w.buf.Len()), nil
	case kindGet:
		n, err := w.store.GetObject(ctx, w.bucket, key)
		if err != nil {
			return 0, err
		}
		if n != int64(w.buf.Len()) {
			return 0, fmt.Errorf("get %q: read %d bytes, want %d", key, n, w.buf.Len())
		}
		return n, nil
	default:
		return 0, fmt.Errorf("unknown request kind %d", w.kinds[op])
	}
}

// objectSpec builds a workload over one keyspace of objects of size bytes.
// When prefill is set, Setup writes every key first so that GETs find their
// objects.
func objectSpec(name string, size, count int, label string, env Env, ops []weightedKind, prefill bool) (Spec, error) {
	space, err := keys.NewKeyspace("bench/"+label+"/", count)
	if err != nil {
		return Spec{}, err
	}
	buf, err := payload.NewBuffer(size, env.Seed)
	if err != nil {
		return Spec{}, err
	}
	specOps := make([]OpSpec, len(ops))
	kinds := make([]opKind, len(ops))
	for i, o := range ops {
		specOps[i] = OpSpec{Name: o.name, Weight: o.weight}
		kinds[i] = o.kind
	}
	spec := Spec{
		Name: name,
		Ops:  specOps,
		NewWorker: func(int) Worker {
			return &objectWorker{store: env.Store, bucket: env.Bucket, space: space, buf: buf, kinds: kinds}
		},
	}
	if prefill {
		spec.Setup = func(ctx context.Context) error {
			return prefillKeyspace(ctx, env, space, buf)
		}
	}
	return spec, nil
}

// prefillKeyspace writes every key of space with buf, using env.Concurrency
// parallel requests.
func prefillKeyspace(ctx context.Context, env Env, space keys.Keyspace, buf payload.Buffer) error {
	return parallelFor(ctx, space.Len(), env.Concurrency, func(ctx context.Context, i int) error {
		key := space.Key(i)
		if err := env.Store.PutObject(ctx, env.Bucket, key, buf.Bytes()); err != nil {
			return fmt.Errorf("prefill %q: %w", key, err)
		}
		return nil
	})
}
