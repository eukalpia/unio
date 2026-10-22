package rs

import (
	"fmt"
	"time"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/erasure"
)

// Defaults applied to zero-valued Options fields.
const (
	// DefaultBudget is the buffer memory of a set whose Options carry no
	// budget.
	DefaultBudget = 128 << 20
	// DefaultStallTimeout is how long a write waits for a drive to accept a
	// block before the drive is treated as failed.
	DefaultStallTimeout = 30 * time.Second
	// DefaultHealQueueSize bounds the queue of damaged shards reported by
	// reads.
	DefaultHealQueueSize = 1024
)

// Options configures a Set. The zero value is valid and selects defaults.
type Options struct {
	// StripeSize is the data bytes per stripe for new writes. Zero selects
	// erasure.DefaultStripeSize. Reads use the stripe size recorded in each
	// layout, so changing this value does not affect existing objects.
	StripeSize int
	// Budget bounds the buffer memory of the set. Nil selects a private
	// budget of DefaultBudget bytes. Share one budget between sets to bound
	// the memory of a node.
	Budget *bufpool.Budget
	// StallTimeout is the longest a write waits for one drive to accept a
	// block. A drive that stalls longer is dropped from the write.
	StallTimeout time.Duration
	// HealQueueSize is the capacity of the queue of damaged shards. Zero
	// selects DefaultHealQueueSize.
	HealQueueSize int
}

// withDefaults returns o with defaults applied, or an error for values that
// cannot be defaulted.
func (o Options) withDefaults() (Options, error) {
	if o.StripeSize == 0 {
		o.StripeSize = erasure.DefaultStripeSize
	}
	if o.StripeSize < 0 {
		return o, fmt.Errorf("erasure/rs: stripe size %d must be positive", o.StripeSize)
	}
	if o.Budget == nil {
		o.Budget = bufpool.NewBudget(DefaultBudget)
	}
	if o.StallTimeout == 0 {
		o.StallTimeout = DefaultStallTimeout
	}
	if o.StallTimeout < 0 {
		return o, fmt.Errorf("erasure/rs: stall timeout %v must not be negative", o.StallTimeout)
	}
	if o.HealQueueSize == 0 {
		o.HealQueueSize = DefaultHealQueueSize
	}
	if o.HealQueueSize < 0 {
		return o, fmt.Errorf("erasure/rs: heal queue size %d must not be negative", o.HealQueueSize)
	}
	return o, nil
}
