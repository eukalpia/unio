package group

import (
	"errors"
	"testing"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/meta"
)

// TestMapApplyErrorRetriesRejectedCommands checks that a command Raft rejects
// before it appends the command maps to meta.ErrNotLeader, which the router
// retries at another replica. A leadership transfer rejects commands this way.
// Any other Raft error may come after the append, so it stays outcome unknown.
func TestMapApplyErrorRetriesRejectedCommands(t *testing.T) {
	for _, err := range []error{raft.ErrNotLeader, raft.ErrLeadershipTransferInProgress} {
		if got := mapApplyError(err); !errors.Is(got, meta.ErrNotLeader) {
			t.Errorf("mapApplyError(%v) = %v, want meta.ErrNotLeader", err, got)
		}
	}
	got := mapApplyError(raft.ErrRaftShutdown)
	if errors.Is(got, meta.ErrNotLeader) || !errors.Is(got, ErrOutcomeUnknown) {
		t.Errorf("mapApplyError(ErrRaftShutdown) = %v, want ErrOutcomeUnknown", got)
	}
}
