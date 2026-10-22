package iam

import (
	"errors"

	"github.com/eukalpia/unio/internal/s3err"
)

// Errors returned by management operations. Each carries the S3 code that
// the admin API reports: NoSuchEntity for a missing user or key, and
// EntityAlreadyExists for a duplicate user name.
var (
	// ErrUserExists is returned when creating a user whose name is taken.
	ErrUserExists = s3err.NewSentinel("iam: user already exists", s3err.EntityAlreadyExists)
	// ErrUserDeleting is returned when creating a user whose name is still
	// being deleted. The caller may retry once the deletion has finished.
	ErrUserDeleting = s3err.NewSentinel("iam: user deletion in progress, retry", s3err.ConditionalRequestConflict)
	// ErrUserNotFound is returned for operations on a missing user.
	ErrUserNotFound = s3err.NewSentinel("iam: user not found", s3err.NoSuchEntity)
	// ErrAccessKeyNotFound is returned for operations on a missing key.
	ErrAccessKeyNotFound = s3err.NewSentinel("iam: access key not found", s3err.NoSuchEntity)
	// ErrInvalidUserName is returned for a user name outside the allowed form.
	ErrInvalidUserName = s3err.NewSentinel("iam: invalid user name", s3err.InvalidArgument)
	// ErrTooManyRetries is returned when compare-and-swap keeps losing to
	// concurrent writers. The request is safe to retry.
	ErrTooManyRetries = s3err.NewSentinel("iam: too many concurrent updates, retry", s3err.ServiceUnavailable)
	// ErrInvalidPolicyName is returned for a managed policy name outside the
	// allowed form.
	ErrInvalidPolicyName = s3err.NewSentinel("iam: invalid policy name", s3err.InvalidArgument)
	// ErrPolicyNotFound is returned for a managed policy that does not exist
	// or whose deletion is in progress, and for an attach of such a policy.
	ErrPolicyNotFound = s3err.NewSentinel("iam: policy not found", s3err.NoSuchEntity)
	// ErrPolicyNotAttached is returned when detaching a policy that the user
	// does not have attached.
	ErrPolicyNotAttached = s3err.NewSentinel("iam: policy is not attached to the user", s3err.NoSuchEntity)
	// ErrPolicyDeleting is returned when replacing a policy whose deletion is
	// in progress. The caller may retry once the deletion has finished.
	ErrPolicyDeleting = s3err.NewSentinel("iam: policy deletion in progress, retry", s3err.ConditionalRequestConflict)
	// ErrPolicyInUse is returned when deleting a managed policy that is still
	// attached to at least one user. Detach it first.
	ErrPolicyInUse = s3err.NewSentinel("iam: policy is attached to a user", s3err.DeleteConflict)
	// ErrPolicyQuota is returned when creating a managed policy would exceed
	// the limit on managed policies.
	ErrPolicyQuota = s3err.NewSentinel("iam: too many managed policies", s3err.InvalidRequest)
	// ErrAttachmentQuota is returned when attaching a policy would exceed the
	// limit on attached policies per user.
	ErrAttachmentQuota = s3err.NewSentinel("iam: too many attached policies", s3err.InvalidRequest)
	// ErrRootPolicy is returned when a policy is attached to, detached from or
	// set on the root identity. Root has full access and no policies.
	ErrRootPolicy = s3err.NewSentinel("iam: the root identity cannot have policies", s3err.InvalidArgument)
)

// errRecordVersion reports a stored record whose version this build does not
// read. It is an internal error.
var errRecordVersion = errors.New("iam: unsupported record version")
