package sigv4

import (
	"crypto/sha256"
	"sync"
)

// keyCacheKey identifies a derived signing key. The secret is stored only
// as its SHA-256 digest, so the cache never holds a raw secret.
type keyCacheKey struct {
	secretDigest [sha256.Size]byte
	date         string
	region       string
}

// keyCache is a bounded cache of derived signing keys. It keeps two
// generations: when the current one reaches size entries it becomes the
// previous one and the older generation is dropped. At most 2*size entries
// are retained, and recently used keys survive. Get is O(1) and needs no
// timers.
type keyCache struct {
	mu   sync.Mutex
	size int
	cur  map[keyCacheKey][sha256.Size]byte
	prev map[keyCacheKey][sha256.Size]byte
}

// newKeyCache returns a cache holding about size entries per generation.
func newKeyCache(size int) *keyCache {
	if size < 1 {
		size = 1
	}
	return &keyCache{size: size, cur: make(map[keyCacheKey][sha256.Size]byte, size)}
}

// get returns the cached key for k. An entry found in the previous
// generation is promoted to the current one.
func (c *keyCache) get(k keyCacheKey) ([sha256.Size]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.cur[k]; ok {
		return v, true
	}
	if v, ok := c.prev[k]; ok {
		c.insertLocked(k, v)
		return v, true
	}
	return [sha256.Size]byte{}, false
}

// put stores the key for k.
func (c *keyCache) put(k keyCacheKey, v [sha256.Size]byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.insertLocked(k, v)
}

// insertLocked adds an entry to the current generation, rotating the
// generations when it is full. The caller holds c.mu.
func (c *keyCache) insertLocked(k keyCacheKey, v [sha256.Size]byte) {
	if _, ok := c.cur[k]; !ok && len(c.cur) >= c.size {
		c.prev = c.cur
		c.cur = make(map[keyCacheKey][sha256.Size]byte, c.size)
	}
	c.cur[k] = v
}
