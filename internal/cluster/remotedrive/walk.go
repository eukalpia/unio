package remotedrive

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/s3err"
)

// Errors of paginated walks.
var (
	// ErrWalkExpired is returned for a cursor the server no longer holds, because
	// it was used up, idled out or was closed. The caller restarts the walk.
	ErrWalkExpired = s3err.NewSentinel("remotedrive: walk cursor expired", s3err.InvalidArgument)
	// ErrTooManyWalks is returned when the server already runs its maximum of
	// concurrent walks.
	ErrTooManyWalks = s3err.NewSentinel("remotedrive: too many concurrent walks", s3err.SlowDown)
)

// walk is a server-side walk session. A goroutine runs drive.WalkShards and
// feeds ch. The channel has a bounded buffer, so the walker waits for the next
// page request, and an idle session is cancelled after WalkIdle.
type walk struct {
	token  string
	drive  ids.DriveID
	ch     chan drive.ShardInfo
	cancel context.CancelFunc
	timer  *time.Timer
	// mu serializes page requests on this session.
	mu sync.Mutex
	// err is the walk's outcome. The walker sets it before closing ch.
	err error
}

// startWalk begins a walk of d for drive id. The caller holds no locks.
func (s *Server) startWalk(id ids.DriveID, d drive.Drive) (*walk, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &walk{
		token:  token,
		drive:  id,
		ch:     make(chan drive.ShardInfo, s.opts.MaxPage),
		cancel: cancel,
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return nil, ErrWalkExpired
	}
	if len(s.walks) >= s.opts.MaxWalks {
		s.mu.Unlock()
		cancel()
		return nil, ErrTooManyWalks
	}
	s.walks[token] = w
	w.timer = time.AfterFunc(s.opts.WalkIdle, func() { s.expireWalk(token) })
	s.mu.Unlock()

	go func() {
		err := d.WalkShards(ctx, func(info drive.ShardInfo) error {
			select {
			case w.ch <- info:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		w.err = err
		close(w.ch)
	}()
	return w, nil
}

// lookupWalk returns the live session for token, which must belong to drive
// id, and renews its idle timer.
func (s *Server) lookupWalk(token string, id ids.DriveID) (*walk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.walks[token]
	if !ok || w.drive != id {
		return nil, ErrWalkExpired
	}
	w.timer.Reset(s.opts.WalkIdle)
	return w, nil
}

// endWalk removes w and stops its walker. It is idempotent.
func (s *Server) endWalk(w *walk) {
	s.mu.Lock()
	if cur, ok := s.walks[w.token]; ok && cur == w {
		delete(s.walks, w.token)
		w.timer.Stop()
	}
	s.mu.Unlock()
	w.cancel()
}

// expireWalk ends the session for token after it idled out.
func (s *Server) expireWalk(token string) {
	s.mu.Lock()
	w, ok := s.walks[token]
	if ok {
		delete(s.walks, token)
	}
	s.mu.Unlock()
	if ok {
		w.cancel()
	}
}

// nextPage collects up to limit shards from w. It returns the page with a
// cursor for the next call, or with Done set once the walk has finished.
func (s *Server) nextPage(ctx context.Context, w *walk, limit int) (*pageResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	page := &pageResult{}
	for len(page.shards) < limit {
		select {
		case info, ok := <-w.ch:
			if !ok {
				s.endWalk(w)
				if w.err != nil {
					return nil, fmt.Errorf("remotedrive: walk: %w", w.err)
				}
				page.done = true
				return page, nil
			}
			page.shards = append(page.shards, info)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	page.cursor = w.token
	return page, nil
}

// pageResult is one page of a walk.
type pageResult struct {
	shards []drive.ShardInfo
	cursor string
	done   bool
}

// newToken returns a random, unguessable cursor token.
func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("remotedrive: walk token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
