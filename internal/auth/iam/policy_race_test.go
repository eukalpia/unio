package iam

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// The tests in this file force the interleavings between AttachUserPolicy and
// DeletePolicy with the hooks of hooks_test.go. Each one names the step it
// interleaves, so a reader can follow the order.

// TestAttachReadPolicyThenDeleteLeavesNoAttachment: the attach has read the
// policy, then the policy is deleted, then the attach writes. The attach must
// find the policy gone after its write and remove its own attachment.
func TestAttachReadPolicyThenDeleteLeavesNoAttachment(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	var ran bool
	kv.arm("get", managedPath("ops"), false, func() {
		if ran {
			return
		}
		ran = true
		if err := s.DeletePolicy(ctx, "ops"); err != nil {
			t.Errorf("delete between the read and the write: %v", err)
		}
	})
	err := s.AttachUserPolicy(ctx, "alice", "ops")
	if !ran {
		t.Fatal("the race was not staged")
	}
	if !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("attach of a policy deleted meanwhile: err = %v, want NoSuchEntity", err)
	}
	if got := mustUserRecord(t, kv, "alice"); len(got.Attached) != 0 {
		t.Fatalf("attachment of a deleted policy left in the user record: %+v", got.Attached)
	}
	expectAllowed(t, s, "alice", false)
	if got := quotaCount(t, kv); got != 0 {
		t.Fatalf("slot count after the delete = %d", got)
	}
}

// TestAttachWrittenThenDeleteIsRefused: the attach has written its attachment,
// then a delete starts. The delete must count the attachment and refuse. The
// attach then confirms the policy, which is live again, and succeeds.
func TestAttachWrittenThenDeleteIsRefused(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	var ran bool
	var deleteErr error
	kv.arm("put", userPath("alice"), false, func() {
		if ran {
			return
		}
		ran = true
		deleteErr = s.DeletePolicy(ctx, "ops")
	})
	if err := s.AttachUserPolicy(ctx, "alice", "ops"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !ran {
		t.Fatal("the race was not staged")
	}
	if !errors.Is(deleteErr, ErrPolicyInUse) {
		t.Fatalf("delete that saw the attachment: err = %v, want DeleteConflict", deleteErr)
	}
	expectAllowed(t, s, "alice", true)
}

// TestDeleteMarkSeenByAttachRollsBack: the attach has written its attachment,
// then the policy is marked as deleting, before the attach confirms it. The
// confirm must see the mark and roll the attachment back.
func TestDeleteMarkSeenByAttachRollsBack(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	var ran bool
	var markErr error
	kv.arm("put", userPath("alice"), false, func() {
		if ran {
			return
		}
		ran = true
		_, markErr = s.markPolicyDeleting(ctx, "ops")
	})
	err := s.AttachUserPolicy(ctx, "alice", "ops")
	if !ran || markErr != nil {
		t.Fatalf("the race was not staged: ran=%v mark=%v", ran, markErr)
	}
	if !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("attach that saw the mark: err = %v, want NoSuchEntity", err)
	}
	if got := mustUserRecord(t, kv, "alice"); len(got.Attached) != 0 {
		t.Fatalf("attachment kept after the mark: %+v", got.Attached)
	}
	// The interrupted deletion can now finish, with nothing to block it.
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete after the rollback: %v", err)
	}
}

// TestRollbackFailureGrantsNothingWhileMarked: the confirm sees the mark, and
// the rollback write fails. The attach reports the error. The attachment stays
// in storage, but the policy is marked, so it grants nothing. Once the policy
// is deleted, the leftover attachment is visible to the count, and detaching
// it clears the way for the delete.
func TestRollbackFailureGrantsNothingWhileMarked(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	var ran bool
	kv.arm("put", userPath("alice"), false, func() {
		if ran {
			return
		}
		ran = true
		if _, err := s.markPolicyDeleting(ctx, "ops"); err != nil {
			t.Errorf("mark: %v", err)
		}
		kv.failNext("put", userPath("alice"), errors.New("storage unavailable"))
	})
	err := s.AttachUserPolicy(ctx, "alice", "ops")
	if !ran {
		t.Fatal("the race was not staged")
	}
	if err == nil || s3err.CodeOf(err) == s3err.NoSuchEntity {
		t.Fatalf("attach whose rollback failed: err = %v, want the storage error", err)
	}
	if got := mustUserRecord(t, kv, "alice"); len(got.Attached) != 1 {
		t.Fatalf("leftover attachment expected after the failed rollback, found %d", len(got.Attached))
	}
	expectAllowed(t, s, "alice", false)

	if err := s.DeletePolicy(ctx, "ops"); !errors.Is(err, ErrPolicyInUse) {
		t.Fatalf("delete with a leftover attachment: err = %v, want DeleteConflict", err)
	}
	if err := s.DetachUserPolicy(ctx, "alice", "ops"); err != nil {
		t.Fatalf("detach of the leftover attachment: %v", err)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete after detach: %v", err)
	}
}

// TestConcurrentAttachDetachAndDelete runs attaches, detaches, deletes,
// replacements and reads of one policy at the same time. Under -race it checks
// that nothing panics or races, that the only errors are the expected ones, and
// that the end state is clean: no user keeps an attachment, and the slot count
// matches the policies that exist.
func TestConcurrentAttachDetachAndDelete(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	users := []string{"u0", "u1", "u2", "u3"}
	for _, u := range users {
		mustCreateUser(t, s, u)
	}
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	expected := func(err error) bool {
		if err == nil {
			return true
		}
		for _, want := range []error{
			ErrPolicyNotFound, ErrPolicyNotAttached, ErrPolicyInUse, ErrPolicyDeleting,
			ErrUserNotFound, ErrTooManyRetries,
		} {
			if errors.Is(err, want) {
				return true
			}
		}
		return false
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var bad []error
	report := func(err error) {
		if !expected(err) {
			mu.Lock()
			bad = append(bad, err)
			mu.Unlock()
		}
	}
	for _, u := range users {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				report(s.AttachUserPolicy(ctx, u, "ops"))
				report(s.DetachUserPolicy(ctx, u, "ops"))
				_, err := s.ListUserPolicies(ctx, u)
				report(err)
			}
		}()
	}
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				report(s.DeletePolicy(ctx, "ops"))
				report(s.PutPolicy(ctx, "ops", []byte(readAllowPolicy)))
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			err := s.Authorize(ctx, auth.Identity{User: users[i%len(users)]}, auth.ActionGetObject, "photos", "a.jpg")
			if err != nil && !s3err.Is(err, s3err.AccessDenied) {
				report(err)
			}
		}
	}()
	wg.Wait()
	for _, err := range bad {
		t.Errorf("unexpected error under concurrency: %v", err)
	}

	// Settle: detach everything, make sure the policy exists, then delete it.
	for _, u := range users {
		if err := s.DetachUserPolicy(ctx, u, "ops"); err != nil && !errors.Is(err, ErrPolicyNotAttached) {
			t.Fatalf("settle detach %s: %v", u, err)
		}
	}
	if err := s.PutPolicy(ctx, "ops", []byte(readAllowPolicy)); err != nil && !errors.Is(err, ErrPolicyDeleting) {
		t.Fatalf("settle put: %v", err)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("settle delete: %v", err)
	}
	for _, u := range users {
		if got := mustUserRecord(t, kv, u); len(got.Attached) != 0 {
			t.Fatalf("user %s keeps attachments after the deletion: %+v", u, got.Attached)
		}
	}
	if left, _ := s.ListPolicies(ctx); len(left) != 0 {
		t.Fatalf("policies left after the settle: %+v", left)
	}
	if got := quotaCount(t, kv); got != 0 {
		t.Fatalf("slot count %d does not match the policies that exist (0)", got)
	}
}

// TestAttachDuringDeleteIsRefused: a delete is counting attachments when an
// attach of the same policy runs. The mark set by the delete must make the
// attach fail before it writes, so the attach cannot succeed against a policy
// that is being deleted, and nothing is left behind.
func TestAttachDuringDeleteIsRefused(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "bob")
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	var ran bool
	var attachErr error
	kv.arm("list", usersPrefix, false, func() {
		if ran {
			return
		}
		ran = true
		attachErr = s.AttachUserPolicy(ctx, "bob", "ops")
	})
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ran {
		t.Fatal("the race was not staged")
	}
	if !errors.Is(attachErr, ErrPolicyNotFound) {
		t.Fatalf("attach during the delete: err = %v, want NoSuchEntity", attachErr)
	}
	if got := mustUserRecord(t, kv, "bob"); len(got.Attached) != 0 {
		t.Fatalf("attachment left by an attach during the delete: %+v", got.Attached)
	}
}
