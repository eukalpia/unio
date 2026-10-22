package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/s3err"
)

// allowAllPolicy grants every action on every resource.
const allowAllPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`

// denyAllPolicy is valid but has no Allow statement, so it never grants access.
const denyAllPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Deny","Action":"s3:*","Resource":"*"}]}`

// mustPutPolicy creates or replaces a managed policy or fails the test.
func mustPutPolicy(t testing.TB, s *Store, name, doc string) {
	t.Helper()
	if err := s.PutPolicy(context.Background(), name, []byte(doc)); err != nil {
		t.Fatalf("put policy %s: %v", name, err)
	}
}

// quotaCount returns the managed policy slot count in storage.
func quotaCount(t testing.TB, kv meta.SystemKV) int {
	t.Helper()
	raw, _, err := kv.Get(context.Background(), quotaKey)
	if errors.Is(err, meta.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	rec, err := decodeQuotaRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return rec.Count
}

// policyID returns the incarnation id stored for a managed policy.
func policyID(t testing.TB, kv meta.SystemKV, name string) string {
	t.Helper()
	raw, _, err := kv.Get(context.Background(), managedPath(name))
	if err != nil {
		t.Fatalf("read policy %s: %v", name, err)
	}
	rec, err := decodeManagedRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return rec.ID
}

// TestPolicyNameRules checks the name rule directly: 1 to 128 characters of
// letters, digits and +=,.@_-.
func TestPolicyNameRules(t *testing.T) {
	valid := []string{
		"a", "0", strings.Repeat("a", maxPolicyNameLen), "Admin+=,.@_-x", "ReadOnly-2026", "a.b@c_d=e+f,g",
	}
	invalid := []string{
		"", strings.Repeat("a", maxPolicyNameLen+1), "a b", "a/b", "a:b", "a*", "a?", "é",
		"a\n", "../x", "a\x00", "a#b", "a$", "a\"", `a\b`, "a(b)",
	}
	for _, name := range valid {
		if !validPolicyName(name) {
			t.Errorf("valid name %q rejected", name)
		}
	}
	for _, name := range invalid {
		if validPolicyName(name) {
			t.Errorf("invalid name %q accepted", name)
		}
	}
}

// TestPutPolicyRejectsInvalidName checks that the store refuses a bad name with
// InvalidArgument, and that a bad name reads as a missing policy.
func TestPutPolicyRejectsInvalidName(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	if err := s.PutPolicy(ctx, "bad/name", []byte(readAllowPolicy)); !s3err.Is(err, s3err.InvalidArgument) || !errors.Is(err, ErrInvalidPolicyName) {
		t.Fatalf("invalid name: err = %v, want InvalidArgument", err)
	}
	if _, err := s.GetPolicy(ctx, "bad/name"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("get of invalid name: err = %v, want NoSuchEntity", err)
	}
	if err := s.DeletePolicy(ctx, "bad/name"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("delete of invalid name: err = %v, want NoSuchEntity", err)
	}
	if left, _ := kv.List(ctx, managedPrefix); len(left) != 0 {
		t.Fatalf("records stored for an invalid name: %d", len(left))
	}
}

// TestPutPolicyValidatesDocument checks that the document goes through
// policy.Parse and the existing size limit. A refused document stores nothing
// and leaves the stored one alone, and it takes no slot.
func TestPutPolicyValidatesDocument(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	mustPutPolicy(t, s, "keep", allowAllPolicy)
	slots := quotaCount(t, kv)

	oversized := allowAllPolicy + strings.Repeat(" ", policy.MaxDocumentSize)
	bad := map[string]string{
		"empty":         "",
		"not json":      `{`,
		"condition":     `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*","Condition":{}}]}`,
		"wrong version": `{"Version":"2008-10-17","Statement":[{"Effect":"Allow","Action":"s3:*","Resource":"*"}]}`,
		"no statement":  `{"Version":"2012-10-17","Statement":[]}`,
		"oversized":     oversized,
	}
	for name, doc := range bad {
		t.Run(name, func(t *testing.T) {
			err := s.PutPolicy(ctx, "keep", []byte(doc))
			if !s3err.Is(err, s3err.MalformedPolicy) {
				t.Fatalf("err = %v, want MalformedPolicy", err)
			}
			got, err := s.GetPolicy(ctx, "keep")
			if err != nil || string(got.Document) != allowAllPolicy {
				t.Fatalf("stored document changed by a refused put: %q, %v", got.Document, err)
			}
		})
	}
	if err := s.PutPolicy(ctx, "fresh", []byte(`{`)); !s3err.Is(err, s3err.MalformedPolicy) {
		t.Fatalf("new name with a malformed document: err = %v", err)
	}
	if _, err := s.GetPolicy(ctx, "fresh"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("refused document stored under a new name: %v", err)
	}
	if got := quotaCount(t, kv); got != slots {
		t.Fatalf("refused documents took slots: %d, was %d", got, slots)
	}

	atLimit := allowAllPolicy + strings.Repeat(" ", policy.MaxDocumentSize-len(allowAllPolicy))
	if len(atLimit) != policy.MaxDocumentSize {
		t.Fatalf("test document is %d bytes, want %d", len(atLimit), policy.MaxDocumentSize)
	}
	if err := s.PutPolicy(ctx, "keep", []byte(atLimit)); err != nil {
		t.Fatalf("document at the size limit refused: %v", err)
	}
}

// TestManagedPolicyLifecycle covers create, read, replace, list and delete.
func TestManagedPolicyLifecycle(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	if _, err := s.GetPolicy(ctx, "ops"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("get of missing policy: err = %v", err)
	}
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	got, err := s.GetPolicy(ctx, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "ops" || string(got.Document) != readAllowPolicy || got.Attachments != 0 {
		t.Fatalf("get = %+v", got)
	}
	mustPutPolicy(t, s, "ops", allowAllPolicy)
	if got, _ := s.GetPolicy(ctx, "ops"); string(got.Document) != allowAllPolicy {
		t.Fatalf("replaced document not returned: %q", got.Document)
	}
	list, err := s.ListPolicies(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "ops" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetPolicy(ctx, "ops"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("get after delete: err = %v", err)
	}
	if list, _ := s.ListPolicies(ctx); len(list) != 0 {
		t.Fatalf("list after delete: %+v", list)
	}
	if err := s.DeletePolicy(ctx, "ops"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("second delete: err = %v, want NoSuchEntity", err)
	}
}

// TestPutPolicyKeepsIncarnationOnReplace checks that a replacement keeps the
// incarnation id, so that attachments stay in place, and that a policy created
// again after a delete gets a new one.
func TestPutPolicyKeepsIncarnationOnReplace(t *testing.T) {
	s, kv, _ := newTestStore(t)
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	first := policyID(t, kv, "ops")
	mustPutPolicy(t, s, "ops", allowAllPolicy)
	if policyID(t, kv, "ops") != first {
		t.Fatal("replacing a document changed the policy's incarnation")
	}
	if err := s.DeletePolicy(context.Background(), "ops"); err != nil {
		t.Fatal(err)
	}
	mustPutPolicy(t, s, "ops", allowAllPolicy)
	if policyID(t, kv, "ops") == first {
		t.Fatal("a policy created after a delete reused the old incarnation")
	}
}

// TestManagedPolicyQuota checks the limit on managed policies: creating one
// beyond it is refused, replacing one is not, and a deleted policy frees its
// slot.
func TestManagedPolicyQuota(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	for i := range maxManagedPolicies {
		mustPutPolicy(t, s, policyName(i), readAllowPolicy)
	}
	if got := quotaCount(t, kv); got != maxManagedPolicies {
		t.Fatalf("slots in use = %d, want %d", got, maxManagedPolicies)
	}

	err := s.PutPolicy(ctx, "one-too-many", []byte(readAllowPolicy))
	if !s3err.Is(err, s3err.InvalidRequest) || !errors.Is(err, ErrPolicyQuota) {
		t.Fatalf("policy beyond the limit: err = %v, want the quota error", err)
	}
	if got := quotaCount(t, kv); got != maxManagedPolicies {
		t.Fatalf("refused create changed the slot count to %d", got)
	}

	mustPutPolicy(t, s, policyName(0), allowAllPolicy)

	if err := s.DeletePolicy(ctx, policyName(7)); err != nil {
		t.Fatal(err)
	}
	if got := quotaCount(t, kv); got != maxManagedPolicies-1 {
		t.Fatalf("slot not given back: %d", got)
	}
	mustPutPolicy(t, s, "one-too-many", readAllowPolicy)
	list, err := s.ListPolicies(ctx)
	if err != nil || len(list) != maxManagedPolicies {
		t.Fatalf("list has %d policies, %v; want %d", len(list), err, maxManagedPolicies)
	}
}

// TestDeletePolicyRefusedWhileAttached checks the conflict: a policy attached
// to a user cannot be deleted, stays in effect when the delete is refused, and
// can be deleted after the user is detached.
func TestDeletePolicyRefusedWhileAttached(t *testing.T) {
	s, kv, _ := newTestStore(t)
	ctx := context.Background()
	mustCreateUser(t, s, "alice")
	mustPutPolicy(t, s, "ops", readAllowPolicy)
	mustAttach(t, s, "alice", "ops")

	err := s.DeletePolicy(ctx, "ops")
	if !s3err.Is(err, s3err.DeleteConflict) || !errors.Is(err, ErrPolicyInUse) {
		t.Fatalf("delete of attached policy: err = %v, want DeleteConflict", err)
	}
	got, err := s.GetPolicy(ctx, "ops")
	if err != nil || got.Attachments != 1 {
		t.Fatalf("after refused delete: %+v, %v; want one attachment", got, err)
	}
	alice := auth.Identity{User: "alice"}
	if err := s.Authorize(ctx, alice, auth.ActionGetObject, "photos", "a.jpg"); err != nil {
		t.Fatalf("refused delete changed access: %v", err)
	}

	if err := s.DetachUserPolicy(ctx, "alice", "ops"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("delete after detach: %v", err)
	}
	if got := quotaCount(t, kv); got != 0 {
		t.Fatalf("slots after delete = %d", got)
	}
}

// TestInterruptedPolicyDeleteCanBeRepeated fails the deletion at the last step.
// The policy must grant nothing and must not be replaceable while the deletion
// is unfinished. A repeat call finishes the job and gives the slot back.
func TestInterruptedPolicyDeleteCanBeRepeated(t *testing.T) {
	s, kv, _ := newHookedStore(t)
	ctx := context.Background()
	mustPutPolicy(t, s, "ops", readAllowPolicy)

	kv.failNext("delete", managedPath("ops"), errors.New("storage unavailable"))
	if err := s.DeletePolicy(ctx, "ops"); err == nil {
		t.Fatal("interrupted delete reported success")
	}
	if err := s.PutPolicy(ctx, "ops", []byte(allowAllPolicy)); !errors.Is(err, ErrPolicyDeleting) {
		t.Fatalf("replacing a policy being deleted: err = %v, want ErrPolicyDeleting", err)
	}
	if _, err := s.GetPolicy(ctx, "ops"); !s3err.Is(err, s3err.NoSuchEntity) {
		t.Fatalf("policy being deleted is still readable: %v", err)
	}
	if list, _ := s.ListPolicies(ctx); len(list) != 0 {
		t.Fatalf("policy being deleted is listed: %+v", list)
	}

	if err := s.DeletePolicy(ctx, "ops"); err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if got := quotaCount(t, kv); got != 0 {
		t.Fatalf("slot not given back by the repeat delete: %d", got)
	}
}

// policyName returns the i-th generated managed policy name.
func policyName(i int) string {
	return fmt.Sprintf("p%04d", i)
}
