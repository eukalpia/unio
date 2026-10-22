package pki

import (
	"sync"
	"time"
)

// Clock is the time source for certificate issuance, renewal and
// verification. Production code uses SystemClock; tests inject a ManualClock.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// AfterFunc calls f after d has elapsed, on its own goroutine, unless the
	// returned Timer is stopped first.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc call.
type Timer interface {
	// Stop prevents the call. It reports whether the call was prevented.
	Stop() bool
}

// SystemClock returns the wall clock.
func SystemClock() Clock { return systemClock{} }

// systemClock is the Clock backed by package time.
type systemClock struct{}

// Now implements Clock.
func (systemClock) Now() time.Time { return time.Now() }

// AfterFunc implements Clock.
func (systemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// ManualClock is a Clock whose time moves only when Advance is called. Timers
// fire during Advance, in deadline order, on the goroutine that called it, and
// the clock's lock is not held while a timer runs, so a timer may call back
// into the clock. ManualClock is for tests.
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTimer
}

// manualTimer is one pending call on a ManualClock.
type manualTimer struct {
	clock *ManualClock
	at    time.Time
	f     func()
	// done is set once the timer has fired or been stopped. Guarded by the
	// clock's lock.
	done bool
}

// NewManualClock returns a clock reading start.
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

// Now implements Clock.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc implements Clock. The call fires on the first Advance that reaches
// now+d.
func (c *ManualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &manualTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward by d, firing every timer that comes due
// along the way. A timer that schedules another due timer fires that one
// too.
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		next := c.nextDueLocked(target)
		if next == nil {
			if target.After(c.now) {
				c.now = target
			}
			c.compactLocked()
			c.mu.Unlock()
			return
		}
		if next.at.After(c.now) {
			c.now = next.at
		}
		next.done = true
		f := next.f
		c.mu.Unlock()
		f()
	}
}

// nextDueLocked returns the pending timer with the earliest deadline at or
// before target, or nil. The caller holds c.mu.
func (c *ManualClock) nextDueLocked(target time.Time) *manualTimer {
	var next *manualTimer
	for _, t := range c.timers {
		if t.done || t.at.After(target) {
			continue
		}
		if next == nil || t.at.Before(next.at) {
			next = t
		}
	}
	return next
}

// compactLocked drops fired and stopped timers. The caller holds c.mu.
func (c *ManualClock) compactLocked() {
	live := c.timers[:0]
	for _, t := range c.timers {
		if !t.done {
			live = append(live, t)
		}
	}
	c.timers = live
}

// Stop implements Timer.
func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}
