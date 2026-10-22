package iam

import (
	"sync"
	"time"
)

// ttlCache is a bounded cache whose entries expire after a TTL. It keeps two
// generations, like the signing-key cache: when the current one is full it
// becomes the previous one and the older generation is dropped. Retention is
// therefore at most 2*size entries. Expired entries are dropped on access.
//
// An epoch counter fences fills against invalidation. Every remove and clear
// advances the epoch. A reader takes the epoch before it loads a value from
// storage and inserts with putAt, which succeeds only if the epoch is
// unchanged. A load that raced with an invalidation therefore cannot put the
// value it read back into the cache.
type ttlCache[K comparable, V any] struct {
	mu    sync.Mutex
	clock func() time.Time
	ttl   time.Duration
	size  int
	epo   uint64
	cur   map[K]ttlEntry[V]
	prev  map[K]ttlEntry[V]
}

type ttlEntry[V any] struct {
	value   V
	expires time.Time
}

// newTTLCache returns a cache of about size entries per generation.
func newTTLCache[K comparable, V any](clock func() time.Time, ttl time.Duration, size int) *ttlCache[K, V] {
	if size < 1 {
		size = 1
	}
	return &ttlCache[K, V]{
		clock: clock,
		ttl:   ttl,
		size:  size,
		cur:   make(map[K]ttlEntry[V]),
	}
}

// get returns the value for k when it is cached and not expired.
func (c *ttlCache[K, V]) get(k K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	e, ok := c.cur[k]
	if !ok {
		if e, ok = c.prev[k]; ok {
			c.insertLocked(k, e)
		}
	}
	if !ok {
		var zero V
		return zero, false
	}
	if !now.Before(e.expires) {
		delete(c.cur, k)
		delete(c.prev, k)
		var zero V
		return zero, false
	}
	return e.value, true
}

// epoch returns the current invalidation epoch. A reader takes it before it
// loads a value, and passes it to putAt.
func (c *ttlCache[K, V]) epoch() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.epo
}

// putAt caches v for k, unless an invalidation has happened since epoch was
// taken. It reports whether v was cached.
func (c *ttlCache[K, V]) putAt(k K, v V, epoch uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epo != epoch {
		return false
	}
	c.insertLocked(k, ttlEntry[V]{value: v, expires: c.clock().Add(c.ttl)})
	return true
}

// remove drops k from the cache and fences in-flight fills.
func (c *ttlCache[K, V]) remove(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epo++
	delete(c.cur, k)
	delete(c.prev, k)
}

// clear drops every entry and fences in-flight fills. It is used after
// administrative changes, which can affect entries for other keys, such as
// all keys of a user.
func (c *ttlCache[K, V]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epo++
	c.cur = make(map[K]ttlEntry[V])
	c.prev = nil
}

// insertLocked stores an entry in the current generation, rotating when it is
// full. The caller holds c.mu.
func (c *ttlCache[K, V]) insertLocked(k K, e ttlEntry[V]) {
	if _, ok := c.cur[k]; !ok && len(c.cur) >= c.size {
		c.prev = c.cur
		c.cur = make(map[K]ttlEntry[V], c.size)
	}
	c.cur[k] = e
}
