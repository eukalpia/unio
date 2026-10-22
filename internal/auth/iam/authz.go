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
	if id.Anonymous || id.User == "" {
		return accessDenied()
	}
	ul, err := s.userLookup(ctx, id.User)
	if err != nil {
		return err
	}
	if !ul.found || !ul.entry.enabled {
		return accessDenied()
	}
	if policy.EvaluateAll(ul.entry.pols, action, resourceARN(bucket, key)) != policy.Allow {
		return accessDenied()
	}
	return nil
}

// accessDenied returns the client-facing denial. It never says which rule
// refused the request.
func accessDenied() error {
	return s3err.Errorf(s3err.AccessDenied, "Access Denied")
}

// resourceARN returns the ARN that policies match against: arn:aws:s3:::bucket
// for a bucket, arn:aws:s3:::bucket/key for an object, and "*" for a request
// with no bucket.
func resourceARN(bucket, key string) string {
	switch {
	case bucket == "":
		return "*"
	case key == "":
		return "arn:aws:s3:::" + bucket
	default:
		return "arn:aws:s3:::" + bucket + "/" + key
	}
}
