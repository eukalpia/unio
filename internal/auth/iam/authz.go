package iam

import (
	"context"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/s3err"
)

// Authorize implements auth.Authorizer. Root is always allowed. Anonymous
// requests and disabled or unknown users are denied. Otherwise the user's
// inline policy and attached managed policies are evaluated together: the
// request is allowed when one of their statements allows it, and no statement
// in any of them denies it. Everything else is denied.
func (s *Store) Authorize(ctx context.Context, id auth.Identity, action auth.Action, bucket, key string) error {
	if id.Root {
		return nil
	}
	d, err := s.Decide(ctx, id, action, bucket, key)
	if err != nil {
		return err
	}
	if d != policy.Allow {
		return accessDenied()
	}
	return nil
}

// Decide returns what the identity policies of id say about action on the
// resource: Allow, Implicit when nothing matched, or Deny. It lets a caller
// combine them with a bucket policy. Root is Allow. Anonymous identities
// have no identity policies and get Implicit. A user that is unknown or
// disabled gets Deny, so no bucket policy can let it in.
func (s *Store) Decide(ctx context.Context, id auth.Identity, action auth.Action, bucket, key string) (policy.Decision, error) {
	if id.Root {
		return policy.Allow, nil
	}
	if id.Anonymous || id.User == "" {
		return policy.Implicit, nil
	}
	ul, err := s.userLookup(ctx, id.User)
	if err != nil {
		return policy.Deny, err
	}
	if !ul.found || !ul.entry.enabled {
		return policy.Deny, nil
	}
	return policy.EvaluateAll(ul.entry.pols, action, ResourceARN(bucket, key)), nil
}

// accessDenied returns the client-facing denial. It never says which rule
// refused the request.
func accessDenied() error {
	return s3err.Errorf(s3err.AccessDenied, "Access Denied")
}

// ResourceARN returns the ARN that policies match against: arn:aws:s3:::bucket
// for a bucket, arn:aws:s3:::bucket/key for an object, and "*" for a request
// with no bucket.
func ResourceARN(bucket, key string) string {
	switch {
	case bucket == "":
		return "*"
	case key == "":
		return "arn:aws:s3:::" + bucket
	default:
		return "arn:aws:s3:::" + bucket + "/" + key
	}
}
