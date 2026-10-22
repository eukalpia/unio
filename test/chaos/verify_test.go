package chaos

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/smithy-go"
)

// fakeObjects is an in-memory ObjectClient. A key absent from the map answers
// NoSuchKey, as the real store does.
type fakeObjects struct {
	objects map[string]fakeObject
}

type fakeObject struct {
	body []byte
	err  error
}

func (f fakeObjects) Get(ctx context.Context, key string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obj, ok := f.objects[key]
	if !ok {
		return nil, &smithy.GenericAPIError{Code: "NoSuchKey", Message: "not found"}
	}
	if obj.err != nil {
		return nil, obj.err
	}
	return obj.body, nil
}

// ledgerEntry builds a ledger entry for a payload written as (seed, seq).
func ledgerEntry(seed, seq uint64, outcome Outcome) Entry {
	p := NewPayload(seed, seq)
	return Entry{
		Key:     Key("run", seq),
		Seq:     seq,
		Size:    len(p.Body),
		Digest:  p.Digest,
		Outcome: outcome,
	}
}

func TestVerify(t *testing.T) {
	const seed = 11
	good := NewPayload(seed, 1)
	other := NewPayload(seed, 2)
	flipped := append([]byte(nil), good.Body...)
	flipped[len(flipped)/2] ^= 0xff
	transport := errors.New("connection reset")

	tests := []struct {
		name      string
		entry     Entry
		object    *fakeObject // nil means the key is absent
		wantInv   string      // empty means no violation
		wantCheck bool        // whether the entry counts as verified
	}{
		{
			name:      "acked and intact",
			entry:     ledgerEntry(seed, 1, OutcomeAcked),
			object:    &fakeObject{body: good.Body},
			wantCheck: true,
		},
		{
			name:      "acked but missing",
			entry:     ledgerEntry(seed, 1, OutcomeAcked),
			object:    nil,
			wantInv:   "I1",
			wantCheck: true,
		},
		{
			name:      "acked but read failed",
			entry:     ledgerEntry(seed, 1, OutcomeAcked),
			object:    &fakeObject{err: transport},
			wantInv:   "I1",
			wantCheck: true,
		},
		{
			name:      "acked with wrong length",
			entry:     ledgerEntry(seed, 1, OutcomeAcked),
			object:    &fakeObject{body: good.Body[:len(good.Body)-1]},
			wantInv:   "I2",
			wantCheck: true,
		},
		{
			name:      "acked with flipped byte",
			entry:     ledgerEntry(seed, 1, OutcomeAcked),
			object:    &fakeObject{body: flipped},
			wantInv:   "I2",
			wantCheck: true,
		},
		{
			name:      "acked with another object's bytes",
			entry:     ledgerEntry(seed, 1, OutcomeAcked),
			object:    &fakeObject{body: other.Body},
			wantInv:   "I2",
			wantCheck: true,
		},
		{
			name:      "rejected and absent",
			entry:     ledgerEntry(seed, 3, OutcomeRejected),
			object:    nil,
			wantCheck: true,
		},
		{
			name:      "rejected but visible",
			entry:     ledgerEntry(seed, 3, OutcomeRejected),
			object:    &fakeObject{body: good.Body},
			wantInv:   "I4",
			wantCheck: true,
		},
		{
			name:      "rejected, absence not confirmed (missing bucket)",
			entry:     ledgerEntry(seed, 3, OutcomeRejected),
			object:    &fakeObject{err: &smithy.GenericAPIError{Code: "NoSuchBucket"}},
			wantInv:   "I4",
			wantCheck: true,
		},
		{
			name:      "rejected, read failed",
			entry:     ledgerEntry(seed, 3, OutcomeRejected),
			object:    &fakeObject{err: transport},
			wantInv:   "I4",
			wantCheck: true,
		},
		{
			name:      "unknown outcome is excluded even when visible",
			entry:     ledgerEntry(seed, 4, OutcomeUnknown),
			object:    &fakeObject{body: good.Body},
			wantCheck: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objects := map[string]fakeObject{}
			if tt.object != nil {
				objects[tt.entry.Key] = *tt.object
			}
			ledger := NewLedger()
			ledger.Record(tt.entry)

			report, err := Verify(context.Background(), fakeObjects{objects}, ledger)
			if err != nil {
				t.Fatalf("Verify: %v", err)
			}
			if tt.wantCheck != (report.Verified == 1) {
				t.Fatalf("verified = %d, want checked=%v", report.Verified, tt.wantCheck)
			}
			if tt.wantInv == "" {
				if len(report.Violations) != 0 {
					t.Fatalf("unexpected violations: %+v", report.Violations)
				}
				return
			}
			if len(report.Violations) != 1 {
				t.Fatalf("violations = %+v, want one %s", report.Violations, tt.wantInv)
			}
			v := report.Violations[0]
			if v.Invariant != tt.wantInv || v.Key != tt.entry.Key || v.Reason == "" {
				t.Fatalf("violation = %+v, want invariant %s on %s", v, tt.wantInv, tt.entry.Key)
			}
		})
	}
}

func TestVerifyReportCounts(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ledgerEntry(5, 1, OutcomeAcked))
	ledger.Record(ledgerEntry(5, 2, OutcomeAcked))
	ledger.Record(ledgerEntry(5, 3, OutcomeRejected))
	ledger.Record(ledgerEntry(5, 4, OutcomeUnknown))

	objects := map[string]fakeObject{
		Key("run", 1): {body: NewPayload(5, 1).Body},
		Key("run", 2): {body: NewPayload(5, 2).Body},
	}
	report, err := Verify(context.Background(), fakeObjects{objects}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if report.Acked != 2 || report.Rejected != 1 || report.Unknown != 1 {
		t.Errorf("counts acked=%d rejected=%d unknown=%d", report.Acked, report.Rejected, report.Unknown)
	}
	if report.Verified != 3 {
		t.Errorf("verified = %d, want 3", report.Verified)
	}
	if len(report.Violations) != 0 {
		t.Errorf("violations = %+v", report.Violations)
	}
}

func TestVerifyEmptyLedger(t *testing.T) {
	report, err := Verify(context.Background(), fakeObjects{}, NewLedger())
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified != 0 || len(report.Violations) != 0 {
		t.Fatalf("report = %+v", report)
	}
}

func TestVerifyViolationsAreSorted(t *testing.T) {
	ledger := NewLedger()
	for seq := uint64(9); seq >= 1; seq-- {
		ledger.Record(ledgerEntry(3, seq, OutcomeAcked))
	}
	report, err := Verify(context.Background(), fakeObjects{}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Violations) != 9 {
		t.Fatalf("violations = %d, want 9", len(report.Violations))
	}
	for i := 1; i < len(report.Violations); i++ {
		if report.Violations[i-1].Key > report.Violations[i].Key {
			t.Fatalf("violations not sorted by key at %d", i)
		}
	}
}

func TestVerifyCancelled(t *testing.T) {
	ledger := NewLedger()
	ledger.Record(ledgerEntry(1, 1, OutcomeAcked))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(ctx, fakeObjects{}, ledger); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify error = %v, want context.Canceled", err)
	}
}
