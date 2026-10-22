package raftstore_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anishathalye/porcupine"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/meta/raftstore"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// Parameters of the linearizability workload. The chaos loop kills and
// restarts one node per round, so at most one node is down at a time.
const (
	linBucket       = "lin-bucket"
	linKeys         = 3
	linClients      = 3
	linOpsPerClient = 60
	linChaosRounds  = 4
	linOpTimeout    = 10 * time.Second
	linCheckTimeout = 2 * time.Minute
)

// linKind is the kind of a workload operation.
type linKind uint8

// Workload operation kinds.
const (
	linPut linKind = iota
	linGet
	linDelete
)

// linInput is the invocation of one operation.
type linInput struct {
	kind  linKind
	key   string
	value string
}

// linOutput is the response of a read. Writes have no response to check.
type linOutput struct {
	found bool
	value string
}

// linModel is a register per key. The state is the current value, and the
// empty string means the key is absent. Writes always apply: a write whose
// outcome is unknown is recorded with a return time after every other event,
// so the checker may place it at any point after its call, including after
// the whole history where it is invisible to every read.
func linModel() porcupine.Model {
	return porcupine.Model{
		Partition: func(history []porcupine.Operation) [][]porcupine.Operation {
			byKey := make(map[string][]porcupine.Operation)
			for _, op := range history {
				key := op.Input.(linInput).key
				byKey[key] = append(byKey[key], op)
			}
			parts := make([][]porcupine.Operation, 0, len(byKey))
			for _, ops := range byKey {
				parts = append(parts, ops)
			}
			return parts
		},
		Init: func() interface{} { return "" },
		Step: func(state, input, output interface{}) (bool, interface{}) {
			cur := state.(string)
			in := input.(linInput)
			switch in.kind {
			case linPut:
				return true, in.value
			case linDelete:
				return true, ""
			default:
				out := output.(linOutput)
				if out.found {
					return out.value == cur && cur != "", cur
				}
				return cur == "", cur
			}
		},
	}
}

// linHistory records operations with logical timestamps from one clock.
type linHistory struct {
	clock   atomic.Int64
	unknown atomic.Int64

	mu  sync.Mutex
	ops []porcupine.Operation
}

// stamp returns the next logical timestamp.
func (h *linHistory) stamp() int64 { return h.clock.Add(1) }

// add records a completed operation.
func (h *linHistory) add(client int, in linInput, call int64, out linOutput) {
	op := porcupine.Operation{ClientId: client, Input: in, Call: call, Output: out, Return: h.stamp()}
	h.mu.Lock()
	h.ops = append(h.ops, op)
	h.mu.Unlock()
}

// addUnknown records a write whose outcome is unknown. Its return time is
// larger than every real timestamp and distinct from the others.
func (h *linHistory) addUnknown(client int, in linInput, call int64) {
	op := porcupine.Operation{
		ClientId: client,
		Input:    in,
		Call:     call,
		Output:   linOutput{},
		Return:   math.MaxInt64 - h.unknown.Add(1),
	}
	h.mu.Lock()
	h.ops = append(h.ops, op)
	h.mu.Unlock()
}

// snapshot returns a copy of the recorded history.
func (h *linHistory) snapshot() []porcupine.Operation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]porcupine.Operation(nil), h.ops...)
}

// hasCode reports whether err carries the S3 code.
func hasCode(err error, code s3err.Code) bool {
	var c s3err.Coder
	return errors.As(err, &c) && c.S3Code() == code
}

// runningStore returns a running store, starting the search at offset.
func (c *testCluster) runningStore(offset int) *raftstore.Store {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.nodes {
		node := c.nodes[(offset+i)%len(c.nodes)]
		if s, ok := c.stores[node]; ok {
			return s
		}
	}
	return nil
}

// linClient runs operations against the cluster and records them. A write
// that fails is recorded as unknown; a read that fails is dropped, since a
// read has no effect.
func linClient(id int, c *testCluster, h *linHistory, progress chan<- struct{}, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < linOpsPerClient; i++ {
		s := c.runningStore(id + i)
		if s == nil {
			return
		}
		key := fmt.Sprintf("k%d", rng.Intn(linKeys))
		call := h.stamp()
		ctx, cancel := context.WithTimeout(context.Background(), linOpTimeout)
		switch rng.Intn(3) {
		case 0:
			value := fmt.Sprintf("c%d-%d", id, i)
			in := linInput{kind: linPut, key: key, value: value}
			_, err := s.PutObject(ctx, meta.ObjectRecord{
				Bucket:  linBucket,
				Key:     key,
				Size:    int64(len(value)),
				ETag:    value,
				ModTime: time.Now(),
				Inline:  []byte(value),
			}, object.Conditions{})
			recordWrite(h, id, in, call, err)
		case 1:
			rec, err := s.GetObject(ctx, linBucket, key, "")
			switch {
			case err == nil:
				h.add(id, linInput{kind: linGet, key: key}, call, linOutput{found: true, value: string(rec.Inline)})
			case hasCode(err, s3err.NoSuchKey):
				h.add(id, linInput{kind: linGet, key: key}, call, linOutput{})
			}
		default:
			in := linInput{kind: linDelete, key: key}
			_, _, err := s.DeleteObject(ctx, linBucket, key, "", object.Conditions{})
			recordWrite(h, id, in, call, err)
		}
		cancel()
		select {
		case progress <- struct{}{}:
		default:
		}
	}
}

// recordWrite records a write as completed or, when it failed, as unknown.
func recordWrite(h *linHistory, client int, in linInput, call int64, err error) {
	if err != nil {
		h.addUnknown(client, in, call)
		return
	}
	h.add(client, in, call, linOutput{})
}

// waitProgress blocks until clients make progress, or reports false once they
// are done.
func waitProgress(progress <-chan struct{}, done <-chan struct{}) bool {
	select {
	case <-progress:
		return true
	case <-done:
		return false
	}
}

// chaos kills and restarts each node in turn. It acts only after the clients
// have made progress, so every round runs against live traffic.
func chaos(t *testing.T, c *testCluster, progress <-chan struct{}, done <-chan struct{}) {
	t.Helper()
	for round := 0; round < linChaosRounds; round++ {
		victim := c.nodes[round%len(c.nodes)]
		if !waitProgress(progress, done) {
			return
		}
		if err := c.kill(victim); err != nil {
			t.Fatalf("chaos kill: %v", err)
		}
		waitProgress(progress, done)
		if err := c.restart(victim); err != nil {
			t.Fatalf("chaos restart: %v", err)
		}
	}
}

// TestLinearizableUnderLeaderFailures runs concurrent clients against a
// three-node cluster while nodes are killed and restarted, and checks that
// the recorded history is linearizable.
func TestLinearizableUnderLeaderFailures(t *testing.T) {
	c := openTestCluster(t, 3)
	ctx, cancel := context.WithTimeout(context.Background(), linCheckTimeout)
	defer cancel()
	if _, err := c.store().CreateBucket(ctx, linBucket, object.MakeBucketOptions{Owner: "lin"}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	h := &linHistory{}
	progress := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for id := 0; id < linClients; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			linClient(id, c, h, progress, int64(id)+1)
		}(id)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	chaos(t, c, progress, done)
	<-done

	ops := h.snapshot()
	if len(ops) == 0 {
		t.Fatal("no operations completed")
	}
	unknown := 0
	for _, op := range ops {
		if op.Return > math.MaxInt64/2 {
			unknown++
		}
	}
	t.Logf("checking %d operations (%d with unknown outcome) across %d kill rounds", len(ops), unknown, linChaosRounds)
	switch porcupine.CheckOperationsTimeout(linModel(), ops, linCheckTimeout) {
	case porcupine.Ok:
	case porcupine.Illegal:
		t.Fatalf("history of %d operations is not linearizable", len(ops))
	default:
		t.Fatalf("linearizability check of %d operations timed out", len(ops))
	}
}
