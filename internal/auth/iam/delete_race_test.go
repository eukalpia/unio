package iam

import (
	"context"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/s3err"
)

// TestDeleteUserCleanupCannotTouchRecreatedUser forces a re-creation of the
// name while DeleteUser is in progress. A re-creation must be refused until
// the deletion has finished, so that the cleanup can never delete the keys or
// the policy of a new user.
func TestDeleteUserCleanupCannotTouchRecreatedUser(t *testing.T) {
	s, kv, clock := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "mia")
	old := mustCreateKey(t, s, "mia")
	if err := s.SetUserPolicy(ctx, "mia", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}

	var createErr, keyErr, policyErr error
	var ran bool
	attempt := func() {
		if ran {
			return
		}
		ran = true
		_, createErr = s.CreateUser(ctx, "mia")
		_, keyErr = s.CreateAccessKey(ctx, "mia")
		policyErr = s.SetUserPolicy(ctx, "mia", []byte(readAllowPolicy))
	}
	kv.arm("put", userPath("mia"), false, attempt)
	kv.arm("delete", userPath("mia"), false, attempt)

	if err := s.DeleteUser(ctx, "mia"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !errors.Is(createErr, ErrUserDeleting) {
		t.Fatalf("re-creating a user during its deletion: err = %v, want ErrUserDeleting", createErr)
	}
	if s3err.CodeOf(keyErr) != s3err.NoSuchEntity {
		t.Fatalf("key for a deleting user: code = %s, want NoSuchEntity", s3err.CodeOf(keyErr))
	}
	if s3err.CodeOf(policyErr) != s3err.NoSuchEntity {
		t.Fatalf("policy for a deleting user: code = %s, want NoSuchEntity", s3err.CodeOf(policyErr))
	}
	// Once the deletion is complete, the name is free again and a new user
	// works. The old key must be gone.
	if _, err := s.Authenticate(ctx, signRequest(t, old.ID, old.Secret, clock.Now())); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("old key authenticates after the delete: %v", err)
	}
	mustCreateUser(t, s, "mia")
	key := mustCreateKey(t, s, "mia")
	if _, err := s.Authenticate(ctx, signRequest(t, key.ID, key.Secret, clock.Now())); err != nil {
		t.Fatalf("new incarnation's key rejected: %v", err)
	}
}

// TestCreateAccessKeyRacingDeleteUserLeavesNoKey deletes the user between the
// write of a new key and the check that the owner still exists. The request
// must fail, and no key for the user may remain.
func TestCreateAccessKeyRacingDeleteUserLeavesNoKey(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "nia")

	var ran bool
	kv.arm("put", keysPrefix, true, func() {
		if ran {
			return
		}
		ran = true
		if err := s.DeleteUser(ctx, "nia"); err != nil {
			t.Errorf("delete during the create: %v", err)
		}
	})
	_, err := s.CreateAccessKey(ctx, "nia")
	if !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("key created for a user deleted during the request: err = %v", err)
	}
	left, _ := kv.List(ctx, keysPrefix)
	for _, kvEntry := range left {
		rec, _ := decodeKeyRecord(kvEntry.Value)
		if rec.User == "nia" {
			t.Fatalf("key %s of a deleted user remains", kvEntry.Key)
		}
	}
}

// TestSetUserPolicyRacingDeleteUserLeavesNoPolicy does the same for a policy.
func TestSetUserPolicyRacingDeleteUserLeavesNoPolicy(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "omar")

	var ran bool
	kv.arm("put", policyPath("omar"), false, func() {
		if ran {
			return
		}
		ran = true
		if err := s.DeleteUser(ctx, "omar"); err != nil {
			t.Errorf("delete during the policy write: %v", err)
		}
	})
	err := s.SetUserPolicy(ctx, "omar", []byte(readAllowPolicy))
	if !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("policy set for a user deleted during the request: err = %v", err)
	}
	if _, _, err := kv.Get(ctx, policyPath("omar")); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("policy of a deleted user remains: %v", err)
	}
}

// TestInterruptedDeleteUserCanBeRepeated fails the deletion partway through.
// The user must already be unable to authenticate, and a repeat call must
// finish the job.
func TestInterruptedDeleteUserCanBeRepeated(t *testing.T) {
	s, kv, clock := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "ned")
	key := mustCreateKey(t, s, "ned")
	req := signRequest(t, key.ID, key.Secret, clock.Now())

	kv.failNext("delete", keyPath(key.ID), errors.New("storage unavailable"))
	if err := s.DeleteUser(ctx, "ned"); err == nil {
		t.Fatal("interrupted delete reported success")
	}
	if _, err := s.Authenticate(ctx, req); !s3err.Is(err, s3err.InvalidAccessKeyID) {
		t.Fatalf("key of a half-deleted user authenticates: %v", err)
	}

	if err := s.DeleteUser(ctx, "ned"); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if left, _ := kv.List(ctx, keysPrefix); len(left) != 0 {
		t.Fatalf("keys left after the repeat delete: %d", len(left))
	}
	if _, _, err := kv.Get(ctx, userPath("ned")); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("user record left after the repeat delete: %v", err)
	}
}

// TestCleanupDeletesOnlyTheNamedIncarnation checks the scoping of the cleanup
// directly: a record of another incarnation of the name is removed, and the
// live incarnation's records are left alone.
func TestCleanupDeletesOnlyTheNamedIncarnation(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "pat")
	live := mustCreateKey(t, s, "pat")
	stale, _ := encodeRecord(keyRecord{V: recordVersion, User: "pat", UserID: "other-incarnation", Secret: []byte("x")})
	if _, err := kv.Put(ctx, keyPath("STALEKEY00000000000A"), stale, 0); err != nil {
		t.Fatal(err)
	}

	if err := s.deleteIncarnation(ctx, "pat", "other-incarnation"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := kv.Get(ctx, keyPath("STALEKEY00000000000A")); !errors.Is(err, meta.ErrNotFound) {
		t.Fatalf("record of the named incarnation survived: %v", err)
	}
	if _, _, err := kv.Get(ctx, keyPath(live.ID)); err != nil {
		t.Fatalf("live incarnation's key was deleted: %v", err)
	}
}

// TestRepeatedDeleteUserCannotRemoveRecreatedUser interrupts a deletion after
// the user's keys are gone, then has a repeat call race with a complete
// deletion and a re-creation of the same name. The repeat call reads the old
// policy, and the policy and the user record it then removes belong to the new
// incarnation. Both must survive.
func TestRepeatedDeleteUserCannotRemoveRecreatedUser(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "quinn")
	key := mustCreateKey(t, s, "quinn")
	if err := s.SetUserPolicy(ctx, "quinn", []byte(readAllowPolicy)); err != nil {
		t.Fatal(err)
	}

	kv.failNext("delete", keyPath(key.ID), errors.New("storage unavailable"))
	if err := s.DeleteUser(ctx, "quinn"); err == nil {
		t.Fatal("interrupted delete reported success")
	}

	var ran bool
	kv.arm("get", policyPath("quinn"), false, func() {
		if ran {
			return
		}
		ran = true
		if err := s.DeleteUser(ctx, "quinn"); err != nil {
			t.Errorf("delete during the repeat: %v", err)
		}
		if _, err := s.CreateUser(ctx, "quinn"); err != nil {
			t.Errorf("re-create during the repeat: %v", err)
		}
		if err := s.SetUserPolicy(ctx, "quinn", []byte(readAllowPolicy)); err != nil {
			t.Errorf("policy for the new user during the repeat: %v", err)
		}
	})
	if err := s.DeleteUser(ctx, "quinn"); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if !ran {
		t.Fatal("the race was not staged")
	}

	if _, _, err := kv.Get(ctx, userPath("quinn")); err != nil {
		t.Fatalf("re-created user's record was removed by the repeat delete: %v", err)
	}
	if _, _, err := kv.Get(ctx, policyPath("quinn")); err != nil {
		t.Fatalf("re-created user's policy was removed by the repeat delete: %v", err)
	}
}
