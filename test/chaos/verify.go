package chaos

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// verifyConcurrency bounds the reads issued by Verify.
const verifyConcurrency = 8

// Invariant identifiers from docs/design/architecture.md section 1.2.
const (
	invariantDurability = "I1"
	invariantIntegrity  = "I2"
	invariantAtomicity  = "I4"
)

// Violation is one broken invariant for one key.
type Violation struct {
	Invariant string
	Key       string
	Reason    string
}

// Report summarises a verification run.
type Report struct {
	// Acked, Rejected and Unknown count the ledger outcomes.
	Acked, Rejected, Unknown int
	// Verified is the number of acked and rejected entries that were read back.
	Verified int
	// Violations is sorted by key, then invariant.
	Violations []Violation
}

// Verify reads back every acknowledged and every rejected write in ledger.
//
// For an acked key, the object must exist and match the recorded length and
// SHA-256 (I1 for existence, I2 for content). For a rejected key, the read
// must return NoSuchKey (I4). Unknown entries are excluded. Read failures
// count as violations, so Verify must run against a fully recovered cluster.
// The error return is only for cancellation.
func Verify(ctx context.Context, client ObjectClient, ledger *Ledger) (Report, error) {
	var report Report
	var checks []Entry
	for _, e := range ledger.Entries() {
		switch e.Outcome {
		case OutcomeAcked:
			report.Acked++
			checks = append(checks, e)
		case OutcomeRejected:
			report.Rejected++
			checks = append(checks, e)
		default:
			report.Unknown++
		}
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(verifyConcurrency)
	for _, e := range checks {
		g.Go(func() error {
			v, err := checkEntry(gctx, client, e)
			if err != nil {
				return err
			}
			if v != nil {
				mu.Lock()
				report.Violations = append(report.Violations, *v)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return Report{}, fmt.Errorf("verify: %w", err)
	}

	report.Verified = len(checks)
	slices.SortFunc(report.Violations, func(a, b Violation) int {
		if c := strings.Compare(a.Key, b.Key); c != 0 {
			return c
		}
		return strings.Compare(a.Invariant, b.Invariant)
	})
	return report, nil
}

// checkEntry reads one key and returns its violation, if any. It returns an
// error only when ctx is done, because then the check could not be completed.
func checkEntry(ctx context.Context, client ObjectClient, e Entry) (*Violation, error) {
	body, err := client.Get(ctx, e.Key)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	switch e.Outcome {
	case OutcomeAcked:
		return checkAcked(e, body, err), nil
	case OutcomeRejected:
		return checkRejected(e, body, err), nil
	default:
		return nil, fmt.Errorf("chaos: entry %s has outcome %s and cannot be verified", e.Key, e.Outcome)
	}
}

func checkAcked(e Entry, body []byte, err error) *Violation {
	switch {
	case isNoSuchKey(err):
		return violationFor(invariantDurability, e, "acknowledged object not found")
	case err != nil:
		return violationFor(invariantDurability, e, "read failed: "+err.Error())
	case len(body) != e.Size:
		return violationFor(invariantIntegrity, e, fmt.Sprintf("length %d, want %d", len(body), e.Size))
	case sha256.Sum256(body) != e.Digest:
		return violationFor(invariantIntegrity, e, "sha256 mismatch")
	}
	return nil
}

func checkRejected(e Entry, body []byte, err error) *Violation {
	switch {
	case err == nil:
		return violationFor(invariantAtomicity, e, fmt.Sprintf("rejected write is visible (%d bytes)", len(body)))
	case isNoSuchKey(err):
		return nil
	default:
		return violationFor(invariantAtomicity, e, "absence not confirmed: "+err.Error())
	}
}

func violationFor(invariant string, e Entry, reason string) *Violation {
	return &Violation{Invariant: invariant, Key: e.Key, Reason: reason}
}
