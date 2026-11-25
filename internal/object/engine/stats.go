package engine

// Stats is a snapshot of the data deletion queue. Deletions of replaced and
// deleted data run in the background. The counters only grow, except
// GCPending, which falls as deletions finish.
//
// The fields are read one after another, so a snapshot taken while deletions
// run may combine values from slightly different moments.
type Stats struct {
	// GCPending is the number of accepted deletions that have not finished.
	GCPending int
	// GCCompleted is the number of deletions that succeeded.
	GCCompleted uint64
	// GCFailed is the number of deletions that returned an error. The data of
	// a failed deletion is left for the scrubber.
	GCFailed uint64
	// GCDropped is the number of deletions that were not accepted, because
	// the backlog was full or the engine was closed. The data of a dropped
	// deletion is left for the scrubber.
	GCDropped uint64
}

// Stats returns the counters of the deletion queue. It is safe for concurrent
// use.
func (e *Engine) Stats() Stats {
	return Stats{
		GCPending:   e.gc.Pending(),
		GCCompleted: e.gc.Completed(),
		GCFailed:    e.gc.Failed(),
		GCDropped:   e.gc.Dropped(),
	}
}
