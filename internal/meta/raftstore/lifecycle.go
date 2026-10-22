package raftstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// Close stops the Raft groups, then closes the state machines, the logs and
// the cache. Ready reports ErrClosed from the moment Close starts. Close is
// safe to call more than once and from several goroutines. Every call returns
// the result of the first.
func (s *Store) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.shutdown() })
	return s.closeErr
}

// shutdown stops the store.
func (s *Store) shutdown() error {
	s.mu.Lock()
	s.closed = true
	groups := s.groups
	s.mu.Unlock()

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
