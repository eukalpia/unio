package group

import (
	"context"
	"sync"

	"github.com/hashicorp/raft"
)

// verifier coalesces leadership checks. A caller that arrives while a
// VerifyLeader round is running cannot reuse that round, because the round
// may have started before the caller's read did. It joins the next round
// instead, and every caller that arrives while a round runs shares that one
// round. Each round is a single heartbeat exchange with the quorum.
//
// A round is answered while raft runs. hashicorp/raft queues a VerifyLeader
// request in a buffered channel that only the running raft loop drains, and a
// request queued as the loop stops is never answered. The verifier therefore
// refuses new rounds once it is closed, and close waits for the rounds already
// started. Its owner calls close before shutting raft down.
type verifier struct {
	r *raft.Raft

	mu      sync.Mutex
	running *verifyRound
	pending *verifyRound

	// gate orders the start of a round against close. A round registers in
	// inflight under the read lock, so close, which takes the write lock, sees
	// every round that started and no round can start after it.
	gate     sync.RWMutex
	closed   bool
	inflight sync.WaitGroup
}

// verifyRound is one VerifyLeader exchange and its outcome.
type verifyRound struct {
	done chan struct{}
	err  error
}

// newVerifier returns a verifier for r.
func newVerifier(r *raft.Raft) *verifier {
	return &verifier{r: r}
}

// verify returns once a leadership check that started after the call
// confirmed that this node is still the leader, or ctx is done. After close,
// a check fails with raft.ErrNotLeader.
func (v *verifier) verify(ctx context.Context) error {
	v.mu.Lock()
	var rd *verifyRound
	if v.running == nil {
		rd = newVerifyRound()
		v.running = rd
		go v.run(rd)
	} else {
		if v.pending == nil {
			v.pending = newVerifyRound()
		}
		rd = v.pending
	}
	v.mu.Unlock()
	select {
	case <-rd.done:
		return rd.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run performs one round, then starts the pending round if one is waiting.
func (v *verifier) run(rd *verifyRound) {
	err := v.leaderCheck()
	v.mu.Lock()
	defer v.mu.Unlock()
	rd.err = err
	close(rd.done)
	v.running = nil
	if v.pending != nil {
		next := v.pending
		v.pending = nil
		v.running = next
		go v.run(next)
	}
}

// leaderCheck issues one VerifyLeader and waits for its answer, or fails at
// once when the verifier is closed.
func (v *verifier) leaderCheck() error {
	fut, ok := v.begin()
	if !ok {
		return raft.ErrNotLeader
	}
	defer v.inflight.Done()
	return fut.Error()
}

// begin registers a round as in flight and queues its VerifyLeader request.
// It reports false, without queuing anything, once the verifier is closed.
func (v *verifier) begin() (raft.Future, bool) {
	v.gate.RLock()
	defer v.gate.RUnlock()
	if v.closed {
		return nil, false
	}
	v.inflight.Add(1)
	return v.r.VerifyLeader(), true
}

// close refuses new rounds and waits for the rounds already in flight. It must
// run while raft is still running, so that every queued request is answered.
func (v *verifier) close() {
	v.gate.Lock()
	v.closed = true
	v.gate.Unlock()
	v.inflight.Wait()
}

// newVerifyRound returns a round that has not completed.
func newVerifyRound() *verifyRound {
	return &verifyRound{done: make(chan struct{})}
}
