package admin

import (
	"container/list"
	"math"
	"net"
	"net/netip"
	"sync"
	"time"
)

// loginLimiter allows each client a fixed number of login attempts per window.
// Each client has a token bucket that holds the whole allowance and refills
// evenly. The buckets live in an LRU of at most maxClients entries, so the
// memory of the limiter is bounded however many addresses try to log in. The
// least recently seen client is forgotten first.
type loginLimiter struct {
	mu         sync.Mutex
	clock      func() time.Time
	burst      float64
	perSecond  float64
	maxClients int
	order      *list.List // front is the most recently used bucket
	entries    map[string]*list.Element
}

// loginBucket is the state of one client.
type loginBucket struct {
	key    string
	tokens float64
	last   time.Time
}

// newLoginLimiter returns a limiter that allows attempts logins per window to
// each client, and tracks at most maxClients clients.
func newLoginLimiter(attempts int, window time.Duration, maxClients int, clock func() time.Time) *loginLimiter {
	return &loginLimiter{
		clock:      clock,
		burst:      float64(attempts),
		perSecond:  float64(attempts) / window.Seconds(),
		maxClients: maxClients,
		order:      list.New(),
		entries:    make(map[string]*list.Element),
	}
}

// allow takes one attempt for key. It returns false, and how long until the
// next attempt is allowed, when the client has used its allowance.
func (l *loginLimiter) allow(key string) (bool, time.Duration) {
	now := l.clock()
	l.mu.Lock()
	defer l.mu.Unlock()

	var b *loginBucket
	if el, ok := l.entries[key]; ok {
		l.order.MoveToFront(el)
		b = el.Value.(*loginBucket)
		// A clock that steps backwards refills nothing rather than draining.
		elapsed := math.Max(0, now.Sub(b.last).Seconds())
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.perSecond)
		b.last = now
	} else {
		b = &loginBucket{key: key, tokens: l.burst, last: now}
		l.entries[key] = l.order.PushFront(b)
		l.evict()
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1 - b.tokens) / l.perSecond * float64(time.Second)))
	return false, wait
}

// evict forgets the least recently used clients beyond the bound. The caller
// holds mu.
func (l *loginLimiter) evict() {
	for l.order.Len() > l.maxClients {
		back := l.order.Back()
		l.order.Remove(back)
		delete(l.entries, back.Value.(*loginBucket).key)
	}
}

// clientKey returns the address the limiter counts a client under. An IPv4
// address is counted alone. An IPv6 client is counted by its /64 network,
// because one host can otherwise rotate through a whole subnet. A remote
// address that does not parse is counted as the text it arrived as.
func clientKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if addr.Is6() {
		prefix, err := addr.Prefix(64)
		if err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}
