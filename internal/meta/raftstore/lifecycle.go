package raftstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta/raftstore/fsm"
	"github.com/eukalpia/unio/internal/meta/raftstore/group"
)

// ErrClosed is returned by Ready and WaitReady once the store is closed.
var ErrClosed = errors.New("raftstore: store is closed")

// Polling bounds of WaitReady, and the number of groups a Ready error names.
const (
	readyPollMin   = 10 * time.Millisecond
	readyPollMax   = 200 * time.Millisecond
	maxNamedGroups = 8
)

// Ready reports whether every group this node hosts, the system group
// included, knows a leader. It returns nil only then. Otherwise it returns an
// error that names up to eight of the groups without a leader and gives their
// total. After Close it returns ErrClosed.
//
// Ready does not wait and does not contact other nodes, so ctx is not
// consulted. It is safe for concurrent use.
func (s *Store) Ready(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ErrClosed
	}
	var missing []ids.PartitionID
	for p, g := range s.groups {
		if _, ok := g.Leader(); !ok {
			missing = append(missing, p)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return noLeaderError(missing)
}

// noLeaderError describes the groups that have no leader. It names the first
// maxNamedGroups of them in ascending order and gives the total.
func noLeaderError(missing []ids.PartitionID) error {
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	var b strings.Builder
	for i, p := range missing {
		if i == maxNamedGroups {
			b.WriteString(", ...")
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(strconv.FormatUint(uint64(p), 10))
	}
	return fmt.Errorf("raftstore: %d groups have no leader: %s", len(missing), b.String())
}

// WaitReady polls Ready until it returns nil. The polls start 10ms apart and
// the gap grows to 200ms. When ctx ends first, WaitReady returns ctx.Err()
// wrapped, together with the last Ready error. When the store is closed it
// returns ErrClosed at once, because Ready can no longer become nil.
func (s *Store) WaitReady(ctx context.Context) error {
	delay := readyPollMin
	for {
		err := s.Ready(ctx)
		if err == nil || errors.Is(err, ErrClosed) {
			return err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("raftstore: wait ready: %w (last state: %v)", ctx.Err(), err)
		case <-timer.C:
		}
		delay = min(2*delay, readyPollMax)
	}
}

// Close shuts the store down. Before it stops the groups, it hands leadership
// of every group this node leads to another voter. The handovers run in
// parallel and are bounded by Options.TransferTimeout in total. A handover
// that fails or does not finish is logged, and Close goes on. Close then stops
// the groups and closes the state machines, the logs and the cache.
//
// Close is safe to call more than once and from several goroutines. Every
// call returns the result of the first.
func (s *Store) Close() error {
	return s.closeWith(true)
}

// closeWith stops the store. When transfer is false it skips the leadership
// handover, as a crash would.
func (s *Store) closeWith(transfer bool) error {
	s.closeOnce.Do(func() { s.closeErr = s.shutdown(transfer) })
	return s.closeErr
}

// shutdown stops the store. Ready reports ErrClosed from the moment it marks
// the store closed, so a node that is leaving stops reporting ready.
func (s *Store) shutdown(transfer bool) error {
	s.mu.Lock()
	s.closed = true
	groups := s.groups
	s.mu.Unlock()

	if transfer {
		s.transferLeadership(groups)
	}
	var errs []error
	for _, g := range groups {
		errs = append(errs, g.Close())
	}
	s.mu.Lock()
	s.groups = map[ids.PartitionID]*group.Group{}
	s.mu.Unlock()

	for _, f := range s.fsms {
		errs = append(errs, f.Close())
	}
	s.fsms = map[ids.PartitionID]*fsm.FSM{}
	if s.logs != nil {
		errs = append(errs, s.logs.Close())
		s.logs = nil
	}
	if s.cache != nil {
		s.cache.Unref()
		s.cache = nil
	}
	return errors.Join(errs...)
}

// transferLeadership hands leadership of each group to another voter. The
// handovers run in parallel and share one deadline of Options.TransferTimeout.
// A group whose handover fails keeps its old leader until Raft elects a new
// one, and the failure is logged.
func (s *Store) transferLeadership(groups map[ids.PartitionID]*group.Group) {
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.TransferTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for p, g := range groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := g.TransferLeadership(ctx); err != nil {
				s.partitionLogger(p).Warn("leadership transfer failed", "error", err)
			}
		}()
	}
	wg.Wait()
}
