package engine_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/object/engine"
	"github.com/eukalpia/unio/internal/object/engine/internal/testbackend"
	"github.com/eukalpia/unio/internal/object/objecttest"
	"github.com/eukalpia/unio/internal/s3err"
)

// errOutcomeUnknown has the shape of the error a consensus group returns when
// a proposal may have been applied (group.ErrOutcomeUnknown).
var errOutcomeUnknown = s3err.NewSentinel("meta: write outcome unknown", s3err.ServiceUnavailable)

// unknownOutcomeStore fails its commits with err once armed. With applied set,
// the commit is made first, so the store holds the new state while the caller
// sees an error, as after a timed-out consensus proposal. Without it, the
// commit is refused before it is applied. Reads are never affected.
type unknownOutcomeStore struct {
	*testbackend.Store
	armed   bool
	applied bool
	err     error
}

func (s *unknownOutcomeStore) PutObject(ctx context.Context, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, error) {
	if !s.armed {
		return s.Store.PutObject(ctx, rec, cond)
	}
	if !s.applied {
		return nil, s.err
	}
	garbage, err := s.Store.PutObject(ctx, rec, cond)
	if err != nil {
		return garbage, err
	}
	return garbage, s.err
}

func (s *unknownOutcomeStore) PutPart(ctx context.Context, bucket, key, uploadID string, part meta.PartRecord) (*meta.PartRecord, error) {
	if !s.armed {
		return s.Store.PutPart(ctx, bucket, key, uploadID, part)
	}
	if !s.applied {
		return nil, s.err
	}
	garbage, err := s.Store.PutPart(ctx, bucket, key, uploadID, part)
	if err != nil {
		return garbage, err
	}
	return garbage, s.err
}

func (s *unknownOutcomeStore) CompleteUpload(ctx context.Context, uploadID string, rec meta.ObjectRecord, cond object.Conditions) ([]meta.ObjectRecord, []meta.PartRecord, error) {
	if !s.armed {
		return s.Store.CompleteUpload(ctx, uploadID, rec, cond)
	}
	if !s.applied {
		return nil, nil, s.err
	}
	garbage, unused, err := s.Store.CompleteUpload(ctx, uploadID, rec, cond)
	if err != nil {
		return garbage, unused, err
	}
	return garbage, unused, s.err
}

// outcome is one way a commit can fail without the engine knowing its result.
var outcomes = []struct {
	name    string
	applied bool
	err     error
}{
	{"deadline after apply", true, context.DeadlineExceeded},
	{"unknown outcome after apply", true, errOutcomeUnknown},
	{"deadline before apply", false, context.DeadlineExceeded},
}

// newOutcomeHarness returns an engine over a store that can be armed later.
func newOutcomeHarness(t *testing.T) (*harness, *unknownOutcomeStore, *testbackend.Registry) {
	t.Helper()
	store := &unknownOutcomeStore{Store: testbackend.NewStore()}
	reg := testbackend.NewRegistry(2, 2, 1)
	h := newHarnessWith(t, store, reg, reg, engine.Options{})
	if _, err := h.MakeBucket(t.Context(), testBucket, object.MakeBucketOptions{Owner: "o"}); err != nil {
		t.Fatal(err)
	}
	return h, store, reg
}

// TestUnknownCommitOutcomeKeepsNewData covers commits whose result the engine
// cannot know, such as a metadata timeout. The data written for such a commit
// must never be deleted. It may be applied, so it is kept and left to the
// scrubber, which checks the metadata before it reclaims anything. Where the
// commit was applied, the object must still read back.
func TestUnknownCommitOutcomeKeepsNewData(t *testing.T) {
	for _, oc := range outcomes {
		t.Run("put/"+oc.name, func(t *testing.T) {
			h, store, reg := newOutcomeHarness(t)
			ctx := t.Context()
			data := payloadOf(5000, 4)
			store.armed, store.applied, store.err = true, oc.applied, oc.err
			if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(data), object.PutOptions{}); err == nil {
				t.Fatal("put with an unknown outcome reported success")
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if n := reg.StoredData(); n != 1 {
				t.Fatalf("stored data units = %d, want 1: the data of an unknown commit was deleted", n)
			}
			if oc.applied {
				if got := mustReadKey(t, h, "k"); !bytes.Equal(got, data) {
					t.Fatal("committed object does not read back")
				}
			}
		})

		t.Run("copy/"+oc.name, func(t *testing.T) {
			h, store, reg := newOutcomeHarness(t)
			ctx := t.Context()
			data := payloadOf(5000, 5)
			if _, err := h.PutObject(ctx, testBucket, "src", objecttest.NewBody(data), object.PutOptions{}); err != nil {
				t.Fatal(err)
			}
			store.armed, store.applied, store.err = true, oc.applied, oc.err
			src := object.Ref{Bucket: testBucket, Key: "src"}
			if _, err := h.CopyObject(ctx, src, testBucket, "dst", object.CopyOptions{}); err == nil {
				t.Fatal("copy with an unknown outcome reported success")
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if n := reg.StoredData(); n != 2 {
				t.Fatalf("stored data units = %d, want 2: the data of an unknown copy was deleted", n)
			}
			if oc.applied {
				if got := mustReadKey(t, h, "dst"); !bytes.Equal(got, data) {
					t.Fatal("committed copy does not read back")
				}
			}
		})

		t.Run("part/"+oc.name, func(t *testing.T) {
			h, store, reg := newOutcomeHarness(t)
			ctx := t.Context()
			uploadID, err := h.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{})
			if err != nil {
				t.Fatal(err)
			}
			data := payloadOf(5000, 6)
			store.armed, store.applied, store.err = true, oc.applied, oc.err
			if _, err := h.PutObjectPart(ctx, testBucket, "k", uploadID, 1, objecttest.NewBody(data)); err == nil {
				t.Fatal("part with an unknown outcome reported success")
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if n := reg.StoredData(); n != 1 {
				t.Fatalf("stored data units = %d, want 1: the data of an unknown part was deleted", n)
			}
		})

		t.Run("complete/"+oc.name, func(t *testing.T) {
			h, store, reg := newOutcomeHarness(t)
			ctx := t.Context()
			uploadID, err := h.NewMultipartUpload(ctx, testBucket, "k", object.PutOptions{})
			if err != nil {
				t.Fatal(err)
			}
			data := payloadOf(5000, 7)
			part, err := h.PutObjectPart(ctx, testBucket, "k", uploadID, 1, objecttest.NewBody(data))
			if err != nil {
				t.Fatal(err)
			}
			store.armed, store.applied, store.err = true, oc.applied, oc.err
			parts := []object.CompletePart{{PartNumber: 1, ETag: part.ETag}}
			if _, err := h.CompleteMultipartUpload(ctx, testBucket, "k", uploadID, parts, object.PutOptions{}); err == nil {
				t.Fatal("completion with an unknown outcome reported success")
			}
			if err := h.Flush(ctx); err != nil {
				t.Fatal(err)
			}
			if n := reg.StoredData(); n != 1 {
				t.Fatalf("stored data units = %d, want 1: the data of an unknown completion was deleted", n)
			}
			if oc.applied {
				if got := mustReadKey(t, h, "k"); !bytes.Equal(got, data) {
					t.Fatal("completed object does not read back")
				}
			}
		})
	}
}

// TestDefinitiveRefusalReclaimsNewData is the control for the test above. When
// the store refuses a commit and says it changed nothing, the data written for
// it is reclaimed, so the other test cannot pass by never deleting anything.
func TestDefinitiveRefusalReclaimsNewData(t *testing.T) {
	h, store, reg := newOutcomeHarness(t)
	ctx := t.Context()
	store.armed, store.applied = true, false
	store.err = s3err.Errorf(s3err.PreconditionFailed, "store: condition failed")
	if _, err := h.PutObject(ctx, testBucket, "k", objecttest.NewBody(payloadOf(5000, 8)), object.PutOptions{}); !s3err.Is(err, s3err.PreconditionFailed) {
		t.Fatalf("put error = %v, want PreconditionFailed", err)
	}
	if err := h.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if n := reg.StoredData(); n != 0 {
		t.Fatalf("stored data units = %d after a definitive refusal, want 0", n)
	}
}
