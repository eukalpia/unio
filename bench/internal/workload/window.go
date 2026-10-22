package workload

import "time"

// window is the measurement interval. Only operations that start and finish
// inside it are measured, so warmup traffic and in-flight work at the end of
// the run never reach the statistics.
type window struct {
	start time.Time
	end   time.Time
}

// verdict is what to do with one completed operation.
type verdict int

const (
	verdictDiscard verdict = iota
	verdictSuccess
	verdictFailure
)

// verdict classifies an operation that ran from start to end. cancelled
// reports whether the run context was done when the operation returned, which
// lets errors caused by the end of the run be told apart from real failures.
func (w window) verdict(start, end time.Time, err error, cancelled bool) verdict {
	switch {
	case start.Before(w.start):
		return verdictDiscard
	case end.After(w.end):
		return verdictDiscard
	case err != nil && cancelled:
		return verdictDiscard
	case err != nil:
		return verdictFailure
	default:
		return verdictSuccess
	}
}

// elapsed returns the measured time up to now, capped at the window end. It is
// zero before the window opens.
func (w window) elapsed(now time.Time) time.Duration {
	if now.After(w.end) {
		now = w.end
	}
	if now.Before(w.start) {
		return 0
	}
	return now.Sub(w.start)
}
