package iam

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/s3err"
)

// hookKV wraps a SystemKV so that a test can force an interleaving. An armed
// hook runs once, after the matching operation has returned, so the caller
// has already observed the old value and has not acted on it yet. A hook
// that runs another store operation does not re-trigger itself.
type hookKV struct {
	meta.SystemKV
	mu    sync.Mutex
	hooks []*kvHook
	fails []*kvFail
}

type kvHook struct {
	op     string
	key    string
	prefix bool
	done   bool
	fn     func()
}

type kvFail struct {
	op  string
	key string
	err error
	// used is set once the failure has been returned.
	used bool
}

// newHookedStore returns a Store over a hookKV, with its clock.
func newHookedStore(t testing.TB) (*Store, *hookKV, *fakeClock) {
	t.Helper()
	clock := newClock()
	kv := &hookKV{SystemKV: authtest.NewMemKV()}
	s, err := New(kv, testConfig(clock))
	if err != nil {
		t.Fatal(err)
	}
	return s, kv, clock
}

// arm runs fn once, after the next op on key (exactly key, or any key with
// that prefix when prefix is set) has returned.
func (h *hookKV) arm(op, key string, prefix bool, fn func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.hooks = append(h.hooks, &kvHook{op: op, key: key, prefix: prefix, fn: fn})
}

// failNext makes the next op on key fail with err, without performing it.
func (h *hookKV) failNext(op, key string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fails = append(h.fails, &kvFail{op: op, key: key, err: err})
}

// matches reports whether an operation on key matches a hook or failure key.
func matches(op, key, wantOp, want string, prefix bool) bool {
	if op != wantOp {
		return false
	}
	if prefix {
		return strings.HasPrefix(key, want)
	}
	return key == want
}

// injected returns the failure to return for op on key, if any.
func (h *hookKV) injected(op, key string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.fails {
		if !f.used && matches(op, key, f.op, f.key, false) {
			f.used = true
			return f.err
		}
	}
	return nil
}

// fire runs the armed hooks for op on key. A hook is marked done before it
// runs, so nested operations cannot fire it again.
func (h *hookKV) fire(op, key string) {
	var run []func()
	h.mu.Lock()
	for _, k := range h.hooks {
		if !k.done && matches(op, key, k.op, k.key, k.prefix) {
			k.done = true
			run = append(run, k.fn)
		}
	}
	h.mu.Unlock()
	for _, fn := range run {
		fn()
	}
}

// Get implements meta.SystemKV.
func (h *hookKV) Get(ctx context.Context, key string) ([]byte, uint64, error) {
	v, rev, err := h.SystemKV.Get(ctx, key)
	h.fire("get", key)
	return v, rev, err
}

// Put implements meta.SystemKV.
func (h *hookKV) Put(ctx context.Context, key string, value []byte, expect uint64) (uint64, error) {
	if err := h.injected("put", key); err != nil {
		return 0, err
	}
	rev, err := h.SystemKV.Put(ctx, key, value, expect)
	h.fire("put", key)
	return rev, err
}

// Delete implements meta.SystemKV.
func (h *hookKV) Delete(ctx context.Context, key string, expect uint64) error {
	if err := h.injected("delete", key); err != nil {
		return err
	}
	err := h.SystemKV.Delete(ctx, key, expect)
	h.fire("delete", key)
	return err
}

// List implements meta.SystemKV.
func (h *hookKV) List(ctx context.Context, prefix string) ([]meta.KV, error) {
	kvs, err := h.SystemKV.List(ctx, prefix)
	h.fire("list", prefix)
	return kvs, err
}

// enableAlice creates alice with a read policy and an access key, and
// returns the key and the identity an authenticated request of it carries.
func enableAlice(t *testing.T, s *Store) (auth.AccessKey, auth.Identity) {
	t.Helper()
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	key := mustCreateKey(t, s, "alice")
	if err := s.SetUserPolicy(ctx, "alice", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}
	return key, auth.Identity{AccessKey: key.ID, User: "alice"}
}

// TestInFlightFillCannotRestoreDisabledUser forces a user read to finish after
// an administrator has disabled the user. The stale enabled state must not
// stay in the cache once the disable has completed.
func TestInFlightFillCannotRestoreDisabledUser(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	_, id := enableAlice(t, s)
	s.clearCaches()

	kv.arm("get", userPath("alice"), false, func() {
		if err := s.SetUserEnabled(ctx, "alice", false); err != nil {
			t.Errorf("disable during the read: %v", err)
		}
	})
	// The reader started before the change, so either answer is acceptable.
	_ = s.Authorize(ctx, id, auth.ActionGetObject, "photos", "a.jpg")

	if err := s.Authorize(ctx, id, auth.ActionGetObject, "photos", "a.jpg"); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("disabled user authorized after the change completed: %v", err)
	}
}

// TestInFlightFillCannotRestoreRevokedPolicy does the same for a policy: the
// stale policy is read, the administrator detaches it, and the read must not
// repopulate the cache.
func TestInFlightFillCannotRestoreRevokedPolicy(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	_, id := enableAlice(t, s)
	s.clearCaches()

	kv.arm("get", policyPath("alice"), false, func() {
		if err := s.SetUserPolicy(ctx, "alice", nil); err != nil {
			t.Errorf("detach during the read: %v", err)
		}
	})
	_ = s.Authorize(ctx, id, auth.ActionGetObject, "photos", "a.jpg")

	if err := s.Authorize(ctx, id, auth.ActionGetObject, "photos", "a.jpg"); !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("revoked policy still authorizes after the change completed: %v", err)
	}
}

// TestInFlightFillCannotRestoreDeletedKey does the same for an access key.
func TestInFlightFillCannotRestoreDeletedKey(t *testing.T) {
	s, kv, clock := newHookedStore(t)
	ctx := context.Background()
	key, _ := enableAlice(t, s)
	s.clearCaches()
	req := signRequest(t, key.ID, key.Secret, clock.Now())

	kv.arm("get", keyPath(key.ID), false, func() {
		if err := s.DeleteAccessKey(ctx, key.ID); err != nil {
			t.Errorf("delete during the read: %v", err)
		}
	})
	_, _ = s.Authenticate(ctx, req)

	if _, err := s.Authenticate(ctx, req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("deleted key authenticates after the delete completed: %v", err)
	}
}
