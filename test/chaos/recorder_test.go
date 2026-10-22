package chaos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// statusErr stands in for an SDK error that carries an HTTP status.
type statusErr struct{ code int }

func (e statusErr) Error() string       { return fmt.Sprintf("status %d", e.code) }
func (e statusErr) HTTPStatusCode() int { return e.code }

func TestClassify(t *testing.T) {
	sdkServerError := &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusInternalServerError}},
		Err:      errors.New("InternalError"),
	}
	tests := []struct {
		name string
		err  error
		want Outcome
	}{
		{"success", nil, OutcomeAcked},
		{"transport failure", errors.New("connection reset by peer"), OutcomeUnknown},
		{"deadline exceeded", context.DeadlineExceeded, OutcomeUnknown},
		{"cancelled", context.Canceled, OutcomeUnknown},
		{"status 200 with error", statusErr{200}, OutcomeUnknown},
		{"status 302", statusErr{302}, OutcomeUnknown},
		{"status 400", statusErr{400}, OutcomeRejected},
		{"status 403", statusErr{403}, OutcomeRejected},
		{"status 404", statusErr{404}, OutcomeRejected},
		{"status 500", statusErr{500}, OutcomeRejected},
		{"status 503 SlowDown", statusErr{503}, OutcomeRejected},
		{"wrapped status 503", fmt.Errorf("put key: %w", statusErr{503}), OutcomeRejected},
		{"SDK response error 500", fmt.Errorf("put: %w", sdkServerError), OutcomeRejected},
		{"API error without status", &smithy.GenericAPIError{Code: "Throttled"}, OutcomeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.err); got != tt.want {
				t.Fatalf("Classify(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestOutcomeString(t *testing.T) {
	tests := map[Outcome]string{
		OutcomeUnknown:  "unknown",
		OutcomeAcked:    "acked",
		OutcomeRejected: "rejected",
	}
	for o, want := range tests {
		if got := o.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", o, got, want)
		}
	}
}

func TestKeyFormat(t *testing.T) {
	if got, want := Key("abc123", 42), "chaos/abc123/42"; got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
}

func TestLedgerCountsAndCopies(t *testing.T) {
	var l Ledger // zero value must be usable
	l.Record(Entry{Key: "k1", Outcome: OutcomeAcked})
	l.Record(Entry{Key: "k2", Outcome: OutcomeRejected})
	l.Record(Entry{Key: "k3", Outcome: OutcomeUnknown, Err: "eof"})
	l.Record(Entry{Key: "k4", Outcome: OutcomeAcked})

	if got := l.Count(OutcomeAcked); got != 2 {
		t.Errorf("acked = %d, want 2", got)
	}
	if got := l.Count(OutcomeRejected); got != 1 {
		t.Errorf("rejected = %d, want 1", got)
	}
	if got := l.Count(OutcomeUnknown); got != 1 {
		t.Errorf("unknown = %d, want 1", got)
	}
	entries := l.Entries()
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	entries[0].Key = "mutated"
	if l.Entries()[0].Key != "k1" {
		t.Fatal("Entries returned the internal slice")
	}
}

func TestLedgerWaitForReturnsWhenAlreadyReached(t *testing.T) {
	l := NewLedger()
	l.Record(Entry{Key: "k", Outcome: OutcomeAcked})
	if err := l.WaitFor(context.Background(), OutcomeAcked, 1); err != nil {
		t.Fatal(err)
	}
}

func TestLedgerWaitForWakesOnRecord(t *testing.T) {
	l := NewLedger()
	done := make(chan error, 1)
	go func() {
		done <- l.WaitFor(context.Background(), OutcomeAcked, 3)
	}()
	for i := range 3 {
		l.Record(Entry{Key: fmt.Sprint(i), Outcome: OutcomeAcked})
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestLedgerWaitForHonoursContext(t *testing.T) {
	l := NewLedger()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- l.WaitFor(ctx, OutcomeAcked, 1)
	}()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitFor error = %v, want context.Canceled", err)
	}
}

func TestLedgerConcurrentRecord(t *testing.T) {
	const writers, perWriter = 8, 200
	l := NewLedger()
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWriter {
				l.Record(Entry{Outcome: OutcomeAcked})
			}
		}()
	}
	wg.Wait()
	if got := l.Count(OutcomeAcked); got != writers*perWriter {
		t.Fatalf("acked = %d, want %d", got, writers*perWriter)
	}
}
