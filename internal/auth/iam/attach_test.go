package iam

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/s3err"
)

// Documents used by the attachment tests.
const (
	getOnlyPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`
	putOnlyPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:PutObject","Resource":"*"}]}`
	denyGetPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:GetObject","Resource":"*"}]}`
)

// mustAttach attaches a managed policy to a user or fails the test.
func mustAttach(t testing.TB, s *Store, user, name string) {
	t.Helper()
	if err := s.AttachUserPolicy(context.Background(), user, name); err != nil {
		t.Fatalf("attach %s to %s: %v", name, user, err)
	}
}

// expectAllowed checks one GetObject on photos/a.jpg for user, and fails the
// test if the answer is not want.
func expectAllowed(t testing.TB, s *Store, user string, want bool) {
	t.Helper()
	err := s.Authorize(context.Background(), auth.Identity{User: user}, auth.ActionGetObject, "photos", "a.jpg")
	got := err == nil
	if err != nil && !s3err.Is(err, s3err.AccessDenied) {
		t.Fatalf("authorize: unexpected error %v", err)
	}
	if got != want {
		t.Fatalf("authorized = %v, want %v", got, want)
	}
}

// mustUserRecord returns the stored user record.
func mustUserRecord(t testing.TB, kv meta.SystemKV, name string) userRecord {
	t.Helper()
	raw, _, err := kv.Get(context.Background(), userPath(name))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := decodeUserRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// TestAttachLifecycle covers attach, the idempotent second attach, the listing
// in name order, the attachment count, and detach.
func TestAttachLifecycle(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "zeta", getOnlyPolicy)
	mustPutPolicy(t, s, "alpha", putOnlyPolicy)

	if names, err := s.ListUserPolicies(ctx, "alice"); err != nil || len(names) != 0 {
		t.Fatalf("new user has policies: %v, %v", names, err)
	}
	mustAttach(t, s, "alice", "zeta")
	mustAttach(t, s, "alice", "alpha")
	mustAttach(t, s, "alice", "zeta")

	names, err := s.ListUserPolicies(ctx, "alice")
	if err != nil || len(names) != 2 || names[0] != "alpha" || names[1] != "zeta" {
		t.Fatalf("list = %v, %v; want [alpha zeta]", names, err)
	}
	got, err := s.GetPolicy(ctx, "zeta")
	if err != nil || got.Attachments != 1 {
		t.Fatalf("attachments of zeta = %+v, %v; attaching twice must count once", got, err)
	}

	if err := s.DetachUserPolicy(ctx, "alice", "zeta"); err != nil {
		t.Fatal(err)
	}
	if err := s.DetachUserPolicy(ctx, "alice", "zeta"); !errors.Is(err, ErrPolicyNotAttached) || !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("detach of a policy not attached: err = %v, want NoSuchEntity", err)
	}
	if names, _ := s.ListUserPolicies(ctx, "alice"); len(names) != 1 || names[0] != "alpha" {
		t.Fatalf("after detach: %v", names)
	}
}

// TestAttachRejectsMissingEntities checks the NoSuchEntity cases.
func TestAttachRejectsMissingEntities(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	cases := []struct {
		name string
		err  error
	}{
		{"attach missing policy", s.AttachUserPolicy(ctx, "alice", "missing")},
		{"attach invalid policy name", s.AttachUserPolicy(ctx, "alice", "bad/name")},
		{"attach to missing user", s.AttachUserPolicy(ctx, "nobody", "ops")},
		{"detach from missing user", s.DetachUserPolicy(ctx, "nobody", "ops")},
		{"detach a policy not attached", s.DetachUserPolicy(ctx, "alice", "ops")},
		{"list for missing user", func() error { _, err := s.ListUserPolicies(ctx, "nobody"); return err }()},
	}
	for _, c := range cases {
		if !s3err.Is(c.err, s3err.NoSuchEntity) {
			t.Errorf("%s: code = %s (%v), want NoSuchEntity", c.name, s3err.CodeOf(c.err), c.err)
		}
	}
}

// TestAttachLimitPerUser checks the limit of ten attached policies per user.
// Attaching one that is already attached does not count against the limit.
func TestAttachLimitPerUser(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	for i := range maxAttachedPolicies + 1 {
		mustPutPolicy(t, s, policyName(i), readAllowPolicy)
	}
	for i := range maxAttachedPolicies {
		mustAttach(t, s, "alice", policyName(i))
	}

	err := s.AttachUserPolicy(ctx, "alice", policyName(maxAttachedPolicies))
	if !s3err.Is(err, s3err.InvalidRequest) || !errors.Is(err, ErrAttachmentQuota) {
		t.Fatalf("attach beyond the limit: err = %v, want the quota error", err)
	}
	mustAttach(t, s, "alice", policyName(3))

	if err := s.DetachUserPolicy(ctx, "alice", policyName(0)); err != nil {
		t.Fatal(err)
	}
	mustAttach(t, s, "alice", policyName(maxAttachedPolicies))
	if names, _ := s.ListUserPolicies(ctx, "alice"); len(names) != maxAttachedPolicies {
		t.Fatalf("attached = %d, want %d", len(names), maxAttachedPolicies)
	}
}

// TestRootCannotHavePolicies checks that the root identity is refused by every
// operation that would give it policies. Root keeps its implicit full access.
func TestRootCannotHavePolicies(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustPutPolicy(t, s, "ops", denyAllPolicy)
	cases := map[string]error{
		"attach":         s.AttachUserPolicy(ctx, testRootKey, "ops"),
		"detach":         s.DetachUserPolicy(ctx, testRootKey, "ops"),
		"inline set":     s.SetUserPolicy(ctx, testRootKey, []byte(readAllowPolicy)),
		"inline removal": s.SetUserPolicy(ctx, testRootKey, nil),
	}
	for name, err := range cases {
		if !s3err.Is(err, s3err.InvalidArgument) || !errors.Is(err, ErrRootPolicy) {
			t.Errorf("%s on root: err = %v, want InvalidArgument", name, err)
		}
	}
	root := auth.Identity{AccessKey: testRootKey, Root: true}
	if err := s.Authorize(ctx, root, auth.ActionDeleteBucket, "photos", ""); err != nil {
		t.Fatalf("root lost full access: %v", err)
	}
}

// TestAuthorizeUnionOfPolicies checks the decision for a user whose inline
// policy and attached managed policies are evaluated together.
func TestAuthorizeUnionOfPolicies(t *testing.T) {
	cases := []struct {
		name    string
		inline  string
		managed []string
		get     bool
		put     bool
		del     bool
	}{
		{"no policy at all", "", nil, false, false, false},
		{"inline allow alone", readAllowPolicy, nil, true, false, false},
		{"managed allow alone", "", []string{putOnlyPolicy}, false, true, false},
		{"managed allow adds to inline", readAllowPolicy, []string{putOnlyPolicy}, true, true, false},
		{"two managed policies union", "", []string{getOnlyPolicy, putOnlyPolicy}, true, true, false},
		{"managed deny overrides inline allow", readAllowPolicy, []string{denyGetPolicy}, false, false, false},
		{"inline deny overrides managed allow", readAllowPolicy, []string{allowAllPolicy}, true, true, false},
		{"managed deny only", "", []string{denyAllPolicy}, false, false, false},
		{"managed allow beside managed deny", "", []string{allowAllPolicy, denyGetPolicy}, false, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _, _ := newTestStore(t)
			ctx := context.Background()
			mustCreateUser(t, s, "u")
			if c.inline != "" {
				if err := s.SetUserPolicy(ctx, "u", []byte(c.inline)); err != nil {
					t.Fatal(err)
				}
			}
			for i, doc := range c.managed {
				name := policyName(i)
				mustPutPolicy(t, s, name, doc)
				mustAttach(t, s, "u", name)
			}
			id := auth.Identity{User: "u"}
			check := func(action auth.Action, key string, want bool) {
				t.Helper()
				err := s.Authorize(ctx, id, action, "photos", key)
				if (err == nil) != want {
					t.Fatalf("%s on %s: allowed = %v, want %v (err %v)", action, key, err == nil, want, err)
				}
			}
			check(auth.ActionGetObject, "a.jpg", c.get)
			check(auth.ActionPutObject, "a.jpg", c.put)
			check(auth.ActionDeleteObject, "a.jpg", c.del)
		})
	}
}

// TestManagedPolicyChangesTakeEffectAtOnceOnThisNode warms the cache, then
// changes the policy through this node. Each change must be visible at once,
// with the clock not advanced, so only the cache clearing can make it so.
func TestManagedPolicyChangesTakeEffectAtOnceOnThisNode(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")
	expectAllowed(t, s, "alice", true)

	mustPutPolicy(t, s, "ops", denyAllPolicy)
	expectAllowed(t, s, "alice", false)

	mustPutPolicy(t, s, "ops", readAllowPolicy)
	expectAllowed(t, s, "alice", true)

	if err := s.DetachUserPolicy(ctx, "alice", "ops"); err != nil {
		t.Fatal(err)
	}
	expectAllowed(t, s, "alice", false)

	mustAttach(t, s, "alice", "ops")
	expectAllowed(t, s, "alice", true)

	// A refused delete takes the mark off again, and the policy stays in
	// effect.
	if err := s.DeletePolicy(ctx, "ops"); !errors.Is(err, ErrPolicyInUse) {
		t.Fatalf("delete while attached: %v", err)
	}
	expectAllowed(t, s, "alice", true)

	if err := s.DetachUserPolicy(ctx, "alice", "ops"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatal(err)
	}
	expectAllowed(t, s, "alice", false)
}

// rewriteStored changes the stored value under key the way another node would,
// going straight to storage. It leaves this node's cache alone.
func rewriteStored(t testing.TB, kv meta.SystemKV, key string, change func(raw []byte) ([]byte, error)) {
	t.Helper()
	ctx := context.Background()
	raw, rev, err := kv.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	next, err := change(raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, key, next, rev); err != nil {
		t.Fatal(err)
	}
}

// setStoredDocument makes another node replace the document of a managed policy.
func setStoredDocument(t testing.TB, kv meta.SystemKV, name, doc string) {
	t.Helper()
	rewriteStored(t, kv, managedPath(name), func(raw []byte) ([]byte, error) {
		rec, err := decodeManagedRecord(raw)
		if err != nil {
			return nil, err
		}
		rec.Document = doc
		return encodeRecord(rec)
	})
}

// clearStoredAttachments makes another node remove every attachment of a user.
func clearStoredAttachments(t testing.TB, kv meta.SystemKV, user string) {
	t.Helper()
	rewriteStored(t, kv, userPath(user), func(raw []byte) ([]byte, error) {
		rec, err := decodeUserRecord(raw)
		if err != nil {
			return nil, err
		}
		rec.Attached = nil
		return encodeRecord(rec)
	})
}

// TestManagedPolicyChangesReachOtherNodesWithinTTL changes the storage the way
// another node would. Each change is seen here only once the cache entry made
// before it has expired.
func TestManagedPolicyChangesReachOtherNodesWithinTTL(t *testing.T) {
	s, kv, clock := newTestStore(t)
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")
	expectAllowed(t, s, "alice", true)

	setStoredDocument(t, kv, "ops", denyAllPolicy)
	expectAllowed(t, s, "alice", true)
	clock.Advance(11 * time.Second)
	expectAllowed(t, s, "alice", false)

	setStoredDocument(t, kv, "ops", readAllowPolicy)
	expectAllowed(t, s, "alice", false)
	clock.Advance(11 * time.Second)
	expectAllowed(t, s, "alice", true)

	clearStoredAttachments(t, kv, "alice")
	expectAllowed(t, s, "alice", true)
	clock.Advance(11 * time.Second)
	expectAllowed(t, s, "alice", false)
}

// TestRecreatedUserInheritsNoAttachments deletes a user with an attachment and
// creates the name again. The new user has no policies, and the attachment no
// longer counts against the policy.
func TestRecreatedUserInheritsNoAttachments(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")
	expectAllowed(t, s, "alice", true)

	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	mustCreateUser(t, s, "alice")
	expectAllowed(t, s, "alice", false)
	if names, err := s.ListUserPolicies(ctx, "alice"); err != nil || len(names) != 0 {
		t.Fatalf("re-created user lists %v, %v", names, err)
	}
	if got, _ := s.GetPolicy(ctx, "ops"); got.Attachments != 0 {
		t.Fatalf("attachments of a deleted user still counted: %d", got.Attachments)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete blocked by an attachment of a deleted user: %v", err)
	}
}

// TestDisabledUserStillCountsAsAttached checks that disabling a user keeps its
// attachments, as AWS does, and that the policy cannot be deleted under it.
func TestDisabledUserStillCountsAsAttached(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")
	if err := s.SetUserEnabled(ctx, "alice", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetPolicy(ctx, "ops"); got.Attachments != 1 {
		t.Fatalf("attachments of a disabled user = %d, want 1", got.Attachments)
	}
	if err := s.DeletePolicy(ctx, "ops"); !errors.Is(err, ErrPolicyInUse) {
		t.Fatalf("delete under a disabled user: %v", err)
	}
	if err := s.SetUserEnabled(ctx, "alice", true); err != nil {
		t.Fatal(err)
	}
	expectAllowed(t, s, "alice", true)
}

// TestStaleAttachmentGrantsNothing reproduces the state that a failed rollback
// or a crash could leave: an attachment whose policy record is gone. The
// attachment grants nothing, and it is not inherited by a policy created later
// under the same name, because that policy has a new incarnation.
func TestStaleAttachmentGrantsNothing(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")
	expectAllowed(t, s, "alice", true)

	if err := kv.Delete(ctx, managedPath("ops"), meta.AnyRevision); err != nil {
		t.Fatal(err)
	}
	// A node that starts after the crash has no cached entries, so the cache is
	// cleared to model that.
	s.clearCaches()
	expectAllowed(t, s, "alice", false)
	if names, _ := s.ListUserPolicies(ctx, "alice"); len(names) != 0 {
		t.Fatalf("stale attachment listed: %v", names)
	}

	mustPutPolicy(t, s, "ops", allowAllPolicy)
	expectAllowed(t, s, "alice", false)
	if names, _ := s.ListUserPolicies(ctx, "alice"); len(names) != 0 {
		t.Fatalf("stale attachment inherited by a new policy: %v", names)
	}
	if got, _ := s.GetPolicy(ctx, "ops"); got.Attachments != 0 {
		t.Fatalf("stale attachment counted: %d", got.Attachments)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("stale attachment blocks delete: %v", err)
	}

	// Attaching the new policy replaces the stale entry rather than adding one.
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")
	expectAllowed(t, s, "alice", true)
	if n := len(mustUserRecord(t, kv, "alice").Attached); n != 1 {
		t.Fatalf("attachments stored = %d, want 1", n)
	}
	if err := s.DetachUserPolicy(ctx, "alice", "ops"); err != nil {
		t.Fatal(err)
	}
}
