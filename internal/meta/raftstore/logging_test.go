package raftstore_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore"
	"github.com/eukalpia/unio/internal/object"
)

// syncBuffer collects log lines written by the raft goroutines of every node.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p to the buffer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// String returns everything written so far.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestLogsCarryMetadataOnly runs a three-node cluster with debug logging and
// writes an object whose bytes are a canary. The logs must name the component
// and the partitions of each group, and must not contain the object bytes in
// any form.
func TestLogsCarryMetadataOnly(t *testing.T) {
	var buf syncBuffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := openTestClusterWith(t, 3, raftstore.Options{HeartbeatTimeout: testHeartbeat, Logger: logger})
	awaitAllReady(t, c)

	const canary = "object-bytes-canary-7f3a9c"
	ctx, cancel := context.WithTimeout(context.Background(), electionWait)
	defer cancel()
	s := c.storeOf(c.nodes[0])
	if _, err := s.CreateBucket(ctx, handoverBkt, object.MakeBucketOptions{Owner: "logs"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	if _, err := s.PutObject(ctx, meta.ObjectRecord{
		Bucket: handoverBkt, Key: "canary", Size: int64(len(canary)), ETag: canary,
		ModTime: time.Now(), Inline: []byte(canary),
	}, object.Conditions{}); err != nil {
		t.Fatalf("put canary: %v", err)
	}
	c.closeAll()
	out := buf.String()

	for _, want := range []string{"component=raft", "partition=0", "partition=1", "entering leader state"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs lack %q", want)
		}
	}
	for _, leaked := range []string{canary, fmt.Sprint([]byte(canary))} {
		if strings.Contains(out, leaked) {
			t.Fatalf("logs contain object bytes %q", leaked)
		}
	}
	goleak.VerifyNone(t)
}
