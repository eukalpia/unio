// Package faults provides fault injection for the local drive engine. It is
// internal to internal/drive/local, so production configuration cannot set
// hooks: only the engine's own tests can.
package faults

// Point names a place in the engine where a fault can be injected.
type Point string

// Injection points.
const (
	// SegmentAppend is consulted before a record is appended to a segment.
	SegmentAppend Point = "segment.append"
	// SegmentSync is consulted before a segment is fsynced.
	SegmentSync Point = "segment.sync"
	// IndexCommit is consulted before an index batch is committed.
	IndexCommit Point = "index.commit"
	// BlobPlace is consulted before a blob is renamed into place.
	BlobPlace Point = "blob.place"
	// ReadData is consulted with the buffer of every packed read that verifies
	// data, so tests can flip bits.
	ReadData Point = "read.data"
)

// Hooks injects faults. A nil *Hooks injects nothing.
type Hooks struct {
	// Fail, if set, is called at each Check. A non-nil error fails the
	// operation at that point.
	Fail func(p Point) error
	// Mutate, if set, may alter the bytes passed to Corrupt.
	Mutate func(p Point, b []byte)
}

// Check returns the injected error for p, if any.
func (h *Hooks) Check(p Point) error {
	if h == nil || h.Fail == nil {
		return nil
	}
	return h.Fail(p)
}

// Corrupt lets the hook alter b at point p.
func (h *Hooks) Corrupt(p Point, b []byte) {
	if h == nil || h.Mutate == nil {
		return
	}
	h.Mutate(p, b)
}
