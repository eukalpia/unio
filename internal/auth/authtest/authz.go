package authtest

import (
	"context"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// AllowAll is an auth.Authorizer that permits every action.
type AllowAll struct{}

var _ auth.Authorizer = AllowAll{}

// Authorize implements auth.Authorizer.
func (AllowAll) Authorize(context.Context, auth.Identity, auth.Action, string, string) error {
	return nil
}

// DenyAll is an auth.Authorizer that refuses every action with AccessDenied.
type DenyAll struct{}

var _ auth.Authorizer = DenyAll{}

// Authorize implements auth.Authorizer.
func (DenyAll) Authorize(context.Context, auth.Identity, auth.Action, string, string) error {
	return s3err.Errorf(s3err.AccessDenied, "access denied")
}
