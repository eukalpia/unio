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
type verifier struct {
	r *raft.Raft

	mu      sync.Mutex
	running *verifyRound
	pending *verifyRound
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
// confirmed that this node is still the leader, or ctx is done.
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
	err := v.r.VerifyLeader().Error()
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

// newVerifyRound returns a round that has not completed.
func newVerifyRound() *verifyRound {
	return &verifyRound{done: make(chan struct{})}
}
