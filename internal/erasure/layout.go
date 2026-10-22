package erasure

import "github.com/eukalpia/unio/internal/ids"

// DefaultStripeSize is the amount of object data per erasure stripe.
const DefaultStripeSize = 1 << 20

// Layout records where the shards of one DataID were written. It is stored
// in object metadata, which makes placement explicit rather than recomputed.
type Layout struct {
	Set          ids.SetID
	DataShards   int
	ParityShards int
	// StripeSize is the number of data bytes per erasure stripe.
	StripeSize int
	// Drives has DataShards+ParityShards entries; entry i is the drive that
	// holds shard i. A zero DriveID marks a shard that was not written.
	Drives []ids.DriveID
}

// TotalShards returns DataShards + ParityShards.
func (l Layout) TotalShards() int { return l.DataShards + l.ParityShards }

// WriteQuorum is the minimum number of shards that must be durable for a
// write to succeed: K, or K+1 when K equals M.
func (l Layout) WriteQuorum() int {
	if l.DataShards == l.ParityShards {
		return l.DataShards + 1
	}
	return l.DataShards
}

// Written returns the number of shards recorded as written.
func (l Layout) Written() int {
	n := 0
	for _, d := range l.Drives {
		if !d.IsZero() {
			n++
		}
	}
	return n
}
