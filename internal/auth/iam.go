package auth

import (
	"context"
	"time"
)

// UserInfo describes an IAM user.
type UserInfo struct {
	Name    string
	Created time.Time
	Enabled bool
}

// AccessKey is a credential pair. Secret is only populated when the key is
// created and is never retrievable afterwards.
type AccessKey struct {
	ID      string
	Secret  string
	User    string
	Created time.Time
}

// Policy is a managed policy: a named document that can be attached to any
// number of users. Document is the policy JSON as stored, and it has been
// validated. Attachments is the number of users it is attached to.
type Policy struct {
	Name        string
	Document    []byte
	Attachments int
}

// IAM manages users, keys and policies on top of meta.SystemKV. Secrets
// are stored encrypted.
type IAM interface {
	Authenticator
	Authorizer
	CreateUser(ctx context.Context, name string) (UserInfo, error)
	DeleteUser(ctx context.Context, name string) error
	// SetUserEnabled enables or disables a user. Requests signed with a
	// disabled user's keys fail authentication.
	SetUserEnabled(ctx context.Context, name string, enabled bool) error
	ListUsers(ctx context.Context) ([]UserInfo, error)
	CreateAccessKey(ctx context.Context, user string) (AccessKey, error)
	DeleteAccessKey(ctx context.Context, accessKeyID string) error
	ListAccessKeys(ctx context.Context, user string) ([]AccessKey, error)
	// SetUserPolicy attaches an IAM policy document (JSON) to a user,
	// replacing any previous one. An empty policy detaches it.
	SetUserPolicy(ctx context.Context, user string, policy []byte) error
}
