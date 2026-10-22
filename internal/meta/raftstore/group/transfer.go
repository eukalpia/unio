package group

import (
	"context"
	"fmt"

	"github.com/hashicorp/raft"
)

// TransferLeadership hands leadership of the group to another voter, so that
// the group elects its next leader at once instead of after an election
// timeout. It returns nil without doing anything when this replica is not the
// leader or when the configuration has no other voter.
//
// Raft bounds the transfer itself by its election timeout, so the call ends
// when Raft answers, or when ctx ends first.
func (g *Group) TransferLeadership(ctx context.Context) error {
	if !g.IsLeader() {
		return nil
	}
	conf := g.raft.GetConfiguration()
	if err := waitFuture(ctx, conf); err != nil {
		return fmt.Errorf("group %d: read configuration: %w", g.id, err)
	}
	if !hasOtherVoter(conf.Configuration(), g.local) {
		return nil
	}
	if err := waitFuture(ctx, g.raft.LeadershipTransfer()); err != nil {
		return fmt.Errorf("group %d: transfer leadership: %w", g.id, err)
	}
	return nil
}

// hasOtherVoter reports whether c has a voter other than local.
func hasOtherVoter(c raft.Configuration, local raft.ServerID) bool {
	for _, s := range c.Servers {
		if s.Suffrage == raft.Voter && s.ID != local {
			return true
		}
	}
	return false
}

// waitFuture waits for f or for ctx to end, whichever comes first. The
// goroutine that waits on f ends once Raft answers f. A transfer that Raft has
// accepted is answered within its election timeout, and the futures are only
// used while the group runs.
func waitFuture(ctx context.Context, f raft.Future) error {
	done := make(chan error, 1)
	go func() { done <- f.Error() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
