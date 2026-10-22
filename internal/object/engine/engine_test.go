package engine_test

import (
	"context"
	"testing"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/engine/internal/testbackend"
	"github.com/eukalpia/unio/internal/object/objecttest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// harness is an engine wired to in-memory backends. It exposes the optional
// capabilities that the conformance suite uses for fault injection and leak
// checks, and the backends themselves for white-box tests.
type harness struct {
	*engine.Engine
	reg   *testbackend.Registry
	store meta.Store
}

// SetWriteQuorumFailure implements objecttest.QuorumFaults.
func (h *harness) SetWriteQuorumFailure(on bool) {
	h.reg.FailWrites(on)
}

// StoredDataCount implements objecttest.DataCounter.
func (h *harness) StoredDataCount() int {
	return h.reg.StoredData()
}

func newHarness(t testing.TB, opts engine.Options) *harness {
	t.Helper()
	reg := testbackend.NewRegistry(2, 2, 1)
	return newHarnessWith(t, testbackend.NewStore(), reg, reg, opts)
}

// newHarnessWith wires an engine to store and sets. reg is the registry whose
// drives hold the data; the harness uses it for fault injection and leak
// checks. sets may wrap reg to change the behaviour of the erasure sets.
func newHarnessWith(t testing.TB, store meta.Store, reg *testbackend.Registry, sets erasure.Registry, opts engine.Options) *harness {
	t.Helper()
	e, err := engine.New(store, sets, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(context.Background()); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})
	return &harness{Engine: e, reg: reg, store: store}
}

func TestEngineConformance(t *testing.T) {
	objecttest.RunLayerSuite(t, func(t *testing.T) object.Layer {
		return newHarness(t, engine.Options{})
	})
}

// recordOf returns the stored record of bucket/key, for white-box checks.
func (h *harness) recordOf(t *testing.T, bucket, key string) meta.ObjectRecord {
	t.Helper()
	rec, err := h.store.GetObject(t.Context(), bucket, key, "")
	if err != nil {
		t.Fatal(err)
	}
	return rec
}
