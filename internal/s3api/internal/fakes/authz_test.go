package fakes

import (
	"context"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
	"github.com/google/go-cmp/cmp"
)

func TestAuthorizerRecordsCalls(t *testing.T) {
	ctx := context.Background()
	id := auth.Identity{AccessKey: FakeAccessKey, User: FakeUser}
	var a Authorizer

	if err := a.Authorize(ctx, id, auth.ActionGetObject, testBucket, "k"); err != nil {
		t.Fatalf("Authorize with nil Deny: %v", err)
	}
	if err := a.Authorize(ctx, auth.Identity{Anonymous: true}, auth.ActionListAllMyBuckets, "", ""); err != nil {
		t.Fatalf("Authorize service action: %v", err)
	}

	want := []Call{
		{Identity: id, Action: auth.ActionGetObject, Bucket: testBucket, Key: "k"},
		{Identity: auth.Identity{Anonymous: true}, Action: auth.ActionListAllMyBuckets},
	}
	if diff := cmp.Diff(want, a.Calls()); diff != "" {
		t.Errorf("Calls mismatch (-want +got):\n%s", diff)
	}
}

func TestAuthorizerDeny(t *testing.T) {
	ctx := context.Background()
	var a Authorizer
	a.Deny = func(id auth.Identity, action auth.Action, bucket, key string) bool {
		return bucket == "secret" && action == auth.ActionPutObject
	}

	err := a.Authorize(ctx, auth.Identity{User: FakeUser}, auth.ActionPutObject, "secret", "k")
	wantCode(t, err, s3err.AccessDenied)
	if err := a.Authorize(ctx, auth.Identity{User: FakeUser}, auth.ActionGetObject, "secret", "k"); err != nil {
		t.Errorf("read of secret bucket denied: %v", err)
	}
	if err := a.Authorize(ctx, auth.Identity{User: FakeUser}, auth.ActionPutObject, "open", "k"); err != nil {
		t.Errorf("write to open bucket denied: %v", err)
	}
	if n := len(a.Calls()); n != 3 {
		t.Errorf("recorded %d calls, want 3 (denied calls are recorded too)", n)
	}
}

func TestAuthorizerCallsReturnsCopy(t *testing.T) {
	ctx := context.Background()
	var a Authorizer
	_ = a.Authorize(ctx, auth.Identity{User: "u"}, auth.ActionGetObject, testBucket, "k")

	got := a.Calls()
	got[0].Key = "mutated"
	if a.Calls()[0].Key != "k" {
		t.Errorf("mutating the returned slice changed the recorded calls")
	}
}

func TestAuthorizerCanceledContext(t *testing.T) {
	var a Authorizer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := a.Authorize(ctx, auth.Identity{}, auth.ActionGetObject, testBucket, "k")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if n := len(a.Calls()); n != 0 {
		t.Errorf("canceled call was recorded (%d calls)", n)
	}
}
