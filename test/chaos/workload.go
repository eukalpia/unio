package chaos

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// WorkloadConfig configures a Workload.
type WorkloadConfig struct {
	// RunID namespaces the keys of this run.
	RunID string
	// Seed derives every payload.
	Seed uint64
	// Workers is the number of concurrent PUT loops.
	Workers int
}

// Workload issues PUTs with unique keys and records every outcome in a Ledger.
type Workload struct {
	cfg     WorkloadConfig
	writers []ObjectWriter
	ledger  *Ledger
	next    atomic.Uint64
}

// NewWorkload validates cfg and returns a Workload that writes through
// writers. Sequence numbers are assigned to writers round-robin.
func NewWorkload(cfg WorkloadConfig, writers []ObjectWriter, ledger *Ledger) (*Workload, error) {
	switch {
	case len(writers) == 0:
		return nil, errors.New("chaos: workload needs at least one writer")
	case cfg.Workers < 1:
		return nil, errors.New("chaos: workload needs at least one worker")
	case cfg.RunID == "":
		return nil, errors.New("chaos: workload needs a run id")
	case ledger == nil:
		return nil, errors.New("chaos: workload needs a ledger")
	}
	return &Workload{
		cfg:     cfg,
		writers: append([]ObjectWriter(nil), writers...),
		ledger:  ledger,
	}, nil
}

// Run starts cfg.Workers PUT loops and returns after ctx is done and every loop
// has finished its current request. Concurrency is bounded by Workers.
func (w *Workload) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range w.cfg.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.work(ctx)
		}()
	}
	wg.Wait()
}

func (w *Workload) work(ctx context.Context) {
	for ctx.Err() == nil {
		seq := w.next.Add(1) - 1
		payload := NewPayload(w.cfg.Seed, seq)
		key := Key(w.cfg.RunID, seq)
		writer := w.writers[seq%uint64(len(w.writers))]

		err := writer.Put(ctx, key, payload.Body)
		entry := Entry{
			Key:     key,
			Seq:     seq,
			Size:    len(payload.Body),
			Digest:  payload.Digest,
			Outcome: Classify(err),
		}
		if err != nil {
			entry.Err = err.Error()
		}
		w.ledger.Record(entry)
	}
}
