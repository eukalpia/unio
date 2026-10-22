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

// IAM manages users, access keys and policies on top of meta.SystemKV.
// Secrets are stored encrypted.
//
// Two kinds of policy exist, as in AWS. An inline policy belongs to one user
// and is set with SetUserPolicy. A managed policy has a name, can be attached
// to any number of users, and is handled by the methods from PutPolicy to
// ListUserPolicies.
//
// Access is the union of a user's inline policy and its attached managed
// policies. A request is allowed when one of their statements allows it and
// no statement in any of them denies it. Otherwise it is denied.
//
// Changes to policies and attachments take effect on the node that makes them
// at once. Other nodes see them within their cache TTL.
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
	// SetUserPolicy sets the inline policy document (JSON) of a user,
	// replacing any previous one. An empty document removes it. It does not
	// touch managed policies. The root identity is refused with
	// InvalidArgument.
	SetUserPolicy(ctx context.Context, user string, policy []byte) error
	// PutPolicy creates the managed policy name with document, or replaces the
	// document of an existing one. The document is validated and must be at
	// most 20 KiB. A bad document fails with MalformedPolicy and stores
	// nothing. Names are 1 to 128 characters of letters, digits and +=,.@_-,
	// and a bad name fails with InvalidArgument. A replacement keeps the
	// policy's attachments. At most 1000 managed policies exist; creating one
	// beyond that fails with InvalidRequest. Replacing a policy whose deletion
	// is in progress fails with ConditionalRequestConflict until the deletion
	// has ended.
	PutPolicy(ctx context.Context, name string, document []byte) error
	// GetPolicy returns a managed policy and the number of users it is
	// attached to. It fails with NoSuchEntity when there is no such policy.
	GetPolicy(ctx context.Context, name string) (Policy, error)
	// ListPolicies returns every managed policy, sorted by name.
	ListPolicies(ctx context.Context) ([]Policy, error)
	// DeletePolicy deletes a managed policy. A policy that is still attached
	// to any user cannot be deleted: the call fails with DeleteConflict until
	// every attachment has been detached. A missing policy fails with
	// NoSuchEntity.
	DeletePolicy(ctx context.Context, name string) error
	// AttachUserPolicy attaches a managed policy to a user. Attaching a policy
	// the user already has succeeds without change. A user may have at most
	// ten attached policies; an attach beyond that fails with InvalidRequest.
	// A missing user or policy fails with NoSuchEntity. The root identity is
	// refused with InvalidArgument.
	AttachUserPolicy(ctx context.Context, user, policyName string) error
	// DetachUserPolicy removes a managed policy from a user. A policy that is
	// not attached fails with NoSuchEntity.
	DetachUserPolicy(ctx context.Context, user, policyName string) error
	// ListUserPolicies returns the names of the managed policies attached to a
	// user, sorted by name.
	ListUserPolicies(ctx context.Context, user string) ([]string, error)
}
