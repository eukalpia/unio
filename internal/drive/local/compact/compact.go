// Package compact reclaims space in sealed segments (architecture section 8).
// A segment whose dead bytes exceed a threshold has its live records copied
// forward and is then retired. The package holds only the policy: which
// segments to visit, the I/O budget, and the loop. The drive supplies the
// mechanics through Target, so the crash-safety rules live with the code that
// owns the index.
package compact

import (
	"context"
	"errors"
	"time"

	"github.com/eukalpia/unio/internal/drive/local/segment"
)

// Errors reported by Target implementations.
var (
	// ErrGone reports that a segment was retired while compaction was using it.
	ErrGone = errors.New("compact: segment gone")
	// ErrLive reports that a segment still holds live records and cannot be
	// retired.
	ErrLive = errors.New("compact: segment still has live records")
)

// DefaultMinDeadRatio is the dead fraction above which a segment is compacted.
const DefaultMinDeadRatio = 0.5

// Segment describes a sealed segment for planning.
type Segment struct {
	ID uint64
	// End is the committed size of the segment in bytes.
	End int64
	// Live is the number of bytes in records the index still references.
	Live int64
}

// DeadRatio returns the fraction of the segment that is garbage.
func (s Segment) DeadRatio() float64 {
	if s.End <= 0 {
		return 0
	}
	return float64(s.End-s.Live) / float64(s.End)
}

// Record is one record of a segment as seen by compaction.
type Record struct {
	Segment uint64
	Offset  int64
	Header  segment.Header
}

// Target is the drive side of compaction. Implementations decide what is live
// and perform every index update; compaction only chooses the order.
type Target interface {
	// Sealed returns the sealed segments in ascending id order.
	Sealed() []Segment
	// Visit calls fn for each record of segment seg in file order. It returns
	// ErrGone if seg was retired. fn must not retain the record.
	Visit(ctx context.Context, seg uint64, fn func(Record) error) error
	// Move keeps rec alive if it is still needed. A live record is copied to
	// the active segment and the index is updated. A tombstone is carried
	// forward when it is still needed. Move returns the bytes appended, which
	// is zero when nothing was needed.
	Move(ctx context.Context, rec Record) (int64, error)
	// Retire deletes segment seg. It returns ErrLive if the segment still holds
	// live records.
	Retire(ctx context.Context, seg uint64) error
}

// Config controls compaction.
type Config struct {
	// MinDeadRatio is the dead fraction above which a segment is compacted.
	// Zero selects DefaultMinDeadRatio.
	MinDeadRatio float64
	// Limiter bounds the bytes read by compaction. Nil means unlimited.
	Limiter *Limiter
	// Interval is how often Run plans a pass without a wake-up. Zero selects
	// one second.
	Interval time.Duration
	// Wake triggers an immediate pass when it receives a value.
	Wake <-chan struct{}
	// OnError receives errors from background passes. It may be nil.
	OnError func(error)
}

// Pass compacts every sealed segment above the dead-ratio threshold, oldest
// first. It returns the number of segments retired. It stops at the first
// error other than ErrGone and ErrLive, and when ctx is done.
func Pass(ctx context.Context, t Target, cfg Config) (int, error) {
	threshold := cfg.MinDeadRatio
	if threshold == 0 {
		threshold = DefaultMinDeadRatio
	}
	retired := 0
	for _, seg := range t.Sealed() {
		if err := ctx.Err(); err != nil {
			return retired, err
		}
		if seg.End == 0 || seg.DeadRatio() <= threshold {
			continue
		}
		err := t.Visit(ctx, seg.ID, func(rec Record) error {
			if err := cfg.Limiter.Wait(ctx, segment.RecordSize(int64(rec.Header.Length))); err != nil {
				return err
			}
			_, err := t.Move(ctx, rec)
			return err
		})
		if errors.Is(err, ErrGone) {
			continue
		}
		if err != nil {
			return retired, err
		}
		switch err := t.Retire(ctx, seg.ID); {
		case errors.Is(err, ErrLive), errors.Is(err, ErrGone):
			continue
		case err != nil:
			return retired, err
		}
		retired++
	}
	return retired, nil
}

// Run runs passes on every wake-up and every interval until ctx is done.
// Errors are reported through cfg.OnError.
func Run(ctx context.Context, t Target, cfg Config) {
	interval := cfg.Interval
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-cfg.Wake:
		}
		if _, err := Pass(ctx, t, cfg); err != nil && ctx.Err() == nil && cfg.OnError != nil {
			cfg.OnError(err)
		}
	}
}
