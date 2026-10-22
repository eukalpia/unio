package chaos

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// Outcome classifies one PUT by what the server told the client.
type Outcome uint8

const (
	// OutcomeUnknown means a transport error or timeout after the request
	// may have reached the server. Neither I1 nor I4 applies, so the write
	// is counted but excluded from verification.
	OutcomeUnknown Outcome = iota
	// OutcomeAcked means the server answered 200. I1 requires the object to
	// survive crashes and to read back intact.
	OutcomeAcked
	// OutcomeRejected means the server answered with an explicit 4xx or 5xx.
	// I4 requires the object to never become visible.
	OutcomeRejected

	outcomeCount
)

// String returns the lower-case name of the outcome.
func (o Outcome) String() string {
	switch o {
	case OutcomeAcked:
		return "acked"
	case OutcomeRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// statusCarrier is implemented by errors that hold an HTTP response status,
// such as the SDK's response errors.
type statusCarrier interface {
	HTTPStatusCode() int
}

// Classify maps the error of a PUT to its Outcome. A nil error is acked. An
// error that carries a 4xx or 5xx status is rejected. Everything else,
// including transport failures, timeouts and statuses outside 4xx and 5xx,
// is unknown.
func Classify(err error) Outcome {
	if err == nil {
		return OutcomeAcked
	}
	var sc statusCarrier
	if errors.As(err, &sc) {
		if code := sc.HTTPStatusCode(); code >= 400 && code <= 599 {
			return OutcomeRejected
		}
	}
	return OutcomeUnknown
}

// Key returns the object key for sequence number seq of a run.
func Key(runID string, seq uint64) string {
	return "chaos/" + runID + "/" + strconv.FormatUint(seq, 10)
}

// Entry is one row of the ledger: a PUT, its payload digest and its outcome.
type Entry struct {
	Key     string
	Seq     uint64
	Size    int
	Digest  [sha256.Size]byte
	Outcome Outcome
	// Err is the error text for a write that was not acknowledged.
	Err string
}

// Ledger records the outcome of every PUT. It is safe for concurrent use. The
// zero value is ready to use.
type Ledger struct {
	mu      sync.Mutex
	entries []Entry
	counts  [outcomeCount]int
	changed chan struct{} // closed and reset on every Record
}

// NewLedger returns an empty Ledger.
func NewLedger() *Ledger {
	return &Ledger{}
}

// Record appends e. An outcome outside the defined set is kept as an entry
// but not counted.
func (l *Ledger) Record(e Entry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	if validOutcome(e.Outcome) {
		l.counts[e.Outcome]++
	}
	if l.changed != nil {
		close(l.changed)
		l.changed = nil
	}
}

// Count returns how many entries have outcome o.
func (l *Ledger) Count(o Outcome) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !validOutcome(o) {
		return 0
	}
	return l.counts[o]
}

// Entries returns a copy of the ledger in the order the writes were recorded.
func (l *Ledger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Entry(nil), l.entries...)
}

// WaitFor blocks until at least n entries have outcome o, or until ctx is
// done. It wakes on each Record instead of polling.
func (l *Ledger) WaitFor(ctx context.Context, o Outcome, n int) error {
	if !validOutcome(o) {
		return fmt.Errorf("chaos: unknown outcome %d", o)
	}
	for {
		l.mu.Lock()
		if l.counts[o] >= n {
			l.mu.Unlock()
			return nil
		}
		if l.changed == nil {
			l.changed = make(chan struct{})
		}
		ch := l.changed
		l.mu.Unlock()

		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for %d %s writes: %w", n, o, ctx.Err())
		case <-ch:
		}
	}
}

func validOutcome(o Outcome) bool {
	return o < outcomeCount
}
