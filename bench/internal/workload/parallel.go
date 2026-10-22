package workload

import (
	"context"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// parallelFor calls fn(ctx, i) for every i in [0, n) using at most workers
// goroutines, and returns the first error. Once an error occurs, indices that
// have not started are skipped.
func parallelFor(ctx context.Context, n, workers int, fn func(ctx context.Context, i int) error) error {
	if n <= 0 {
		return nil
	}
	workers = min(max(workers, 1), n)
	g, gctx := errgroup.WithContext(ctx)
	var next atomic.Int64
	for range workers {
		g.Go(func() error {
			for {
				if err := gctx.Err(); err != nil {
					return err
				}
				i := next.Add(1) - 1
				if i >= int64(n) {
					return nil
				}
				if err := fn(gctx, int(i)); err != nil {
					return err
				}
			}
		})
	}
	return g.Wait()
}
