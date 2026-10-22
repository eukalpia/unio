package admin

import (
	"testing"
	"time"
)

func TestLoginLimiterBurstThenRefill(t *testing.T) {
	clock := &fakeClock{now: testNow}
	l := newLoginLimiter(3, time.Minute, 16, clock.Now)

	for i := 0; i < 3; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("attempt %d refused inside the burst", i+1)
		}
	}
	ok, wait := l.allow("a")
	if ok {
		t.Fatal("fourth attempt allowed")
	}
	if wait != 20*time.Second {
		t.Errorf("wait = %v, want 20s (one token every 20s for 3 a minute)", wait)
	}

	clock.Advance(19 * time.Second)
	if ok, _ := l.allow("a"); ok {
		t.Error("allowed before a token has refilled")
	}
	clock.Advance(2 * time.Second)
	if ok, _ := l.allow("a"); !ok {
		t.Error("refused after a token has refilled")
	}
}

func TestLoginLimiterRefillIsCapped(t *testing.T) {
	clock := &fakeClock{now: testNow}
	l := newLoginLimiter(2, time.Minute, 16, clock.Now)
	clock.Advance(time.Hour)
	for i := 0; i < 2; i++ {
		if ok, _ := l.allow("a"); !ok {
			t.Fatalf("attempt %d refused", i+1)
		}
	}
	if ok, _ := l.allow("a"); ok {
		t.Error("an hour of idleness banked more than the burst")
	}
}

func TestLoginLimiterClientsAreIndependent(t *testing.T) {
	clock := &fakeClock{now: testNow}
	l := newLoginLimiter(1, time.Minute, 16, clock.Now)
	if ok, _ := l.allow("a"); !ok {
		t.Fatal("first attempt of a refused")
	}
	if ok, _ := l.allow("a"); ok {
		t.Fatal("second attempt of a allowed")
	}
	if ok, _ := l.allow("b"); !ok {
		t.Error("client b was refused because of client a")
	}
}

// TestLoginLimiterMemoryIsBounded checks the LRU: the table never holds more
// clients than its cap, and the least recently used client is the one forgotten.
func TestLoginLimiterMemoryIsBounded(t *testing.T) {
	clock := &fakeClock{now: testNow}
	l := newLoginLimiter(1, time.Minute, 3, clock.Now)
	for _, k := range []string{"a", "b", "c"} {
		l.allow(k)
	}
	// Touch a, so that b is now the least recently used.
	clock.Advance(time.Second)
	l.allow("a")
	l.allow("d")
	if n := l.order.Len(); n != 3 {
		t.Fatalf("table holds %d clients, want 3", n)
	}
	if _, ok := l.entries["b"]; ok {
		t.Error("the least recently used client was not evicted")
	}
	for _, k := range []string{"a", "c", "d"} {
		if _, ok := l.entries[k]; !ok {
			t.Errorf("client %s was evicted", k)
		}
	}

	// Many distinct addresses cannot grow the table.
	for i := 0; i < 10000; i++ {
		l.allow(string(rune('A'+i%26)) + itoa(i))
	}
	if n := l.order.Len(); n != 3 {
		t.Fatalf("table holds %d clients after 10000 addresses, want 3", n)
	}
}

func TestLoginLimiterClockBackwardsDoesNotDrain(t *testing.T) {
	clock := &fakeClock{now: testNow}
	l := newLoginLimiter(2, time.Minute, 16, clock.Now)
	l.allow("a")
	clock.Advance(-time.Hour)
	if ok, _ := l.allow("a"); !ok {
		t.Error("a clock stepping backwards refused a client that had a token")
	}
	if ok, _ := l.allow("a"); ok {
		t.Error("a clock stepping backwards refilled a bucket")
	}
}

func TestClientKey(t *testing.T) {
	cases := []struct {
		remote, want string
	}{
		{"192.0.2.10:443", "192.0.2.10"},
		{"192.0.2.10", "192.0.2.10"},
		{"[::ffff:192.0.2.10]:443", "192.0.2.10"},
		{"[2001:db8:1:2:3:4:5:6]:443", "2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff:ffff:ffff:ffff]:443", "2001:db8:1:2::/64"},
		{"not an address", "not an address"},
	}
	for _, tc := range cases {
		if got := clientKey(tc.remote); got != tc.want {
			t.Errorf("clientKey(%q) = %q, want %q", tc.remote, got, tc.want)
		}
	}
}

func TestRetrySeconds(t *testing.T) {
	cases := map[time.Duration]int{
		0:                      1,
		time.Millisecond:       1,
		6 * time.Second:        6,
		6*time.Second + 1:      7,
		59*time.Second + 500e6: 60,
	}
	for in, want := range cases {
		if got := retrySeconds(in); got != want {
			t.Errorf("retrySeconds(%v) = %d, want %d", in, got, want)
		}
	}
}
