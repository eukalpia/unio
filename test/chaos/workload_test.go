package chaos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// scriptedWriter records every key it receives per backend and answers with
// an outcome chosen from the sequence number embedded in the key.
type scriptedWriter struct {
	index int
	mu    *sync.Mutex
	seen  map[string]int // key -> backend index
	reply func(seq uint64) error
}

func (w *scriptedWriter) Put(_ context.Context, key string, _ []byte) error {
	w.mu.Lock()
	w.seen[key] = w.index
	w.mu.Unlock()
	seq, err := seqFromKey(key)
	if err != nil {
		return err
	}
	return w.reply(seq)
}

func seqFromKey(key string) (uint64, error) {
	var seq uint64
	i := strings.LastIndex(key, "/")
	if _, err := fmt.Sscan(key[i+1:], &seq); err != nil {
		return 0, fmt.Errorf("bad key %q: %w", key, err)
	}
	return seq, nil
}

// outcomeFor is the scripted classification: every third write is rejected,
// every fifth is lost in transit, the rest are acknowledged.
func outcomeFor(seq uint64) Outcome {
	switch {
	case seq%3 == 0:
		return OutcomeRejected
	case seq%5 == 0:
		return OutcomeUnknown
	default:
		return OutcomeAcked
	}
}

func scriptedReply(seq uint64) error {
	switch outcomeFor(seq) {
	case OutcomeRejected:
		return statusErr{500}
	case OutcomeUnknown:
		return errors.New("connection reset")
	default:
		return nil
	}
}

func TestNewWorkloadValidation(t *testing.T) {
	ledger := NewLedger()
	writer := &scriptedWriter{mu: &sync.Mutex{}, seen: map[string]int{}, reply: scriptedReply}
	good := WorkloadConfig{RunID: "r1", Seed: 1, Workers: 2}
	tests := []struct {
		name    string
		cfg     WorkloadConfig
		writers []ObjectWriter
		ledger  *Ledger
		wantErr bool
	}{
		{"valid", good, []ObjectWriter{writer}, ledger, false},
		{"no writers", good, nil, ledger, true},
		{"no workers", WorkloadConfig{RunID: "r1", Seed: 1}, []ObjectWriter{writer}, ledger, true},
		{"empty run id", WorkloadConfig{Seed: 1, Workers: 2}, []ObjectWriter{writer}, ledger, true},
		{"nil ledger", good, []ObjectWriter{writer}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewWorkload(tt.cfg, tt.writers, tt.ledger)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewWorkload error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWorkloadRecordsOutcomesAndRoundRobins(t *testing.T) {
	const backends = 3
	const seed = 77
	mu := &sync.Mutex{}
	writers := make([]ObjectWriter, backends)
	seen := make([]*scriptedWriter, backends)
	for i := range writers {
		seen[i] = &scriptedWriter{index: i, mu: mu, seen: map[string]int{}, reply: scriptedReply}
		writers[i] = seen[i]
	}
	ledger := NewLedger()
	wl, err := NewWorkload(WorkloadConfig{RunID: "run-x", Seed: seed, Workers: 4}, writers, ledger)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		wl.Run(ctx)
	}()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer waitCancel()
	if err := ledger.WaitFor(waitCtx, OutcomeAcked, 40); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done

	entries := ledger.Entries()
	if len(entries) < 40 {
		t.Fatalf("recorded %d entries, want at least 40", len(entries))
	}
	keys := map[string]bool{}
	for _, e := range entries {
		if keys[e.Key] {
			t.Fatalf("duplicate key %s", e.Key)
		}
		keys[e.Key] = true
		if want := Key("run-x", e.Seq); e.Key != want {
			t.Fatalf("key = %q, want %q", e.Key, want)
		}
		p := NewPayload(seed, e.Seq)
		if e.Digest != p.Digest || e.Size != len(p.Body) {
			t.Fatalf("entry %d digest/size do not match the seeded payload", e.Seq)
		}
		if got := outcomeFor(e.Seq); e.Outcome != got {
			t.Fatalf("seq %d outcome = %v, want %v", e.Seq, e.Outcome, got)
		}
	}
	for i, w := range seen {
		for key, idx := range w.seen {
			if idx != i {
				t.Fatalf("writer %d saw key %s tagged for %d", i, key, idx)
			}
			seq, err := seqFromKey(key)
			if err != nil {
				t.Fatal(err)
			}
			if int(seq%backends) != i {
				t.Fatalf("seq %d routed to backend %d, want %d", seq, i, seq%backends)
			}
		}
	}
}

func TestWorkloadStopsOnCancel(t *testing.T) {
	writer := &scriptedWriter{mu: &sync.Mutex{}, seen: map[string]int{}, reply: func(uint64) error { return nil }}
	ledger := NewLedger()
	wl, err := NewWorkload(WorkloadConfig{RunID: "r", Seed: 1, Workers: 2}, []ObjectWriter{writer}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wl.Run(ctx) // must return promptly without issuing writes
	if n := ledger.Count(OutcomeAcked) + ledger.Count(OutcomeUnknown) + ledger.Count(OutcomeRejected); n != 0 {
		t.Fatalf("cancelled workload recorded %d writes", n)
	}
}

func TestSeqFromKeyRoundTrip(t *testing.T) {
	got, err := seqFromKey(Key("abc", 123456))
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(uint64(123456), got); diff != "" {
		t.Fatalf("seq mismatch (-want +got):\n%s", diff)
	}
}
