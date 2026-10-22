package fakes

import (
	"context"
	"slices"
	"sync"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// Call is one recorded invocation of Authorizer.Authorize.
type Call struct {
	Identity auth.Identity
	Action   auth.Action
	Bucket   string
	Key      string
}

// Authorizer is a fake auth.Authorizer for tests. It records every call and
// rejects those for which Deny returns true. The zero value allows every call.
// It is safe for concurrent use; set Deny before the Authorizer is shared.
type Authorizer struct {
	// Deny reports whether a call must fail with AccessDenied. A nil Deny
	// allows every call.
	Deny func(id auth.Identity, action auth.Action, bucket, key string) bool

	mu    sync.Mutex
	calls []Call
}

var _ auth.Authorizer = (*Authorizer)(nil)

// Authorize records the call and returns AccessDenied when Deny rejects it.
// A canceled context is returned without recording the call.
func (a *Authorizer) Authorize(ctx context.Context, id auth.Identity, action auth.Action, bucket, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	a.mu.Lock()
	a.calls = append(a.calls, Call{Identity: id, Action: action, Bucket: bucket, Key: key})
	a.mu.Unlock()

	if a.Deny != nil && a.Deny(id, action, bucket, key) {
		return s3err.Errorf(s3err.AccessDenied, "Access Denied")
	}
	return nil
}

// Calls returns a copy of the recorded calls in the order they were made.
func (a *Authorizer) Calls() []Call {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}
