package iam

import (
	"sync"
	"time"
)

// ttlCache is a bounded cache whose entries expire after a TTL. It keeps two
// generations, like the signing-key cache: when the current one is full it
// becomes the previous one and the older generation is dropped. Retention is
// therefore at most 2*size entries. Expired entries are dropped on access.
type ttlCache[K comparable, V any] struct {
	mu    sync.Mutex
	clock func() time.Time
	ttl   time.Duration
	size  int
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

// put caches v for k for the TTL.
func (c *ttlCache[K, V]) put(k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertLocked(k, ttlEntry[V]{value: v, expires: c.clock().Add(c.ttl)})
}

// remove drops k from the cache.
func (c *ttlCache[K, V]) remove(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cur, k)
	delete(c.prev, k)
}

// clear drops every entry. It is used after administrative changes, which
// can affect entries for other keys (for example, all keys of a user).
func (c *ttlCache[K, V]) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
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
