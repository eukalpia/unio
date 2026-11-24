package payload

import (
	"hash"
	"sync"
)

// pipeThreshold is the smallest declared payload hashed off the request
// goroutine. Below it the goroutine hand-off costs more than it saves.
const pipeThreshold = 1 << 20

// slabSize is the unit of work handed to the hashers. Large enough that the
// hand-off is noise next to hashing it, small enough to keep memory low.
const slabSize = 256 << 10

// slabsInFlight bounds the slabs being hashed behind the reader, so a payload
// holds at most slabsInFlight+1 slabs no matter how large it is.
const slabsInFlight = 2

var slabPool = sync.Pool{New: func() any {
	b := make([]byte, 0, slabSize)
	return &b
}}

// hashPipe feeds a payload to its hashers off the request goroutine.
//
// For a large upload the hashes dominate the CPU cost: MD5 for the ETag plus
// the signed SHA-256 and any requested checksum. Computed inline they run one
// after another, and the erasure encoding waits for all of them. Here the
// accepted bytes are copied into slabs, and each slab is hashed by every
// hasher in parallel while the reader goes on, so the request costs as much
// as its slowest hash rather than the sum of all of them.
//
// Slabs are hashed strictly in order: the work for slab k starts only after
// slab k-1 is done, so every hasher sees the bytes in the order they arrived.
// No goroutine waits for future input, so a Body that is abandoned midway
// leaves nothing behind once the slabs already handed over are hashed.
type hashPipe struct {
	hashers []hash.Hash
	pending *[]byte       // slab being filled; nil until the first byte
	tail    chan struct{} // closed when the newest dispatched slab is hashed
	window  []chan struct{}
}

func newHashPipe(hashers []hash.Hash) *hashPipe {
	return &hashPipe{hashers: hashers}
}

// write copies p into slabs, dispatching every full one.
func (h *hashPipe) write(p []byte) {
	for len(p) > 0 {
		if h.pending == nil {
			s := slabPool.Get().(*[]byte)
			*s = (*s)[:0]
			h.pending = s
		}
		buf := *h.pending
		n := copy(buf[len(buf):cap(buf)], p)
		*h.pending = buf[:len(buf)+n]
		p = p[n:]
		if len(*h.pending) == cap(*h.pending) {
			h.dispatch()
		}
	}
}

// dispatch hands the pending slab to the hashers. It waits while
// slabsInFlight slabs are still being hashed, which is the back-pressure that
// keeps the reader from running ahead of slow hashers.
func (h *hashPipe) dispatch() {
	slab := h.pending
	h.pending = nil
	if slab == nil || len(*slab) == 0 {
		if slab != nil {
			slabPool.Put(slab)
		}
		return
	}
	if len(h.window) == slabsInFlight {
		<-h.window[0]
		h.window = h.window[1:]
	}
	prev := h.tail
	done := make(chan struct{})
	go func() {
		if prev != nil {
			<-prev
		}
		data := *slab
		if len(h.hashers) == 1 {
			h.hashers[0].Write(data)
		} else {
			var wg sync.WaitGroup
			for _, hh := range h.hashers[1:] {
				wg.Go(func() { hh.Write(data) })
			}
			h.hashers[0].Write(data)
			wg.Wait()
		}
		slabPool.Put(slab)
		close(done)
	}()
	h.tail = done
	h.window = append(h.window, done)
}

// wait hashes everything written so far and returns when every hasher has
// seen it. The hashers may be read after wait returns.
func (h *hashPipe) wait() {
	h.dispatch()
	if h.tail != nil {
		<-h.tail
	}
	h.window = h.window[:0]
}
