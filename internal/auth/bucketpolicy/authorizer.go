// Package bucketpolicy authorizes requests with the identity policies of the
// caller and the policy of the bucket the request names.
//
// The rule, in order:
//
//  1. Root is always allowed.
//  2. A Deny in the bucket policy refuses everyone else.
//  3. A Deny from the identity policies refuses the request. An unknown or
//     disabled user is a Deny.
//  4. An Allow from either the identity policies or the bucket policy allows
//     the request.
//  5. Anything else is refused.
//
// Bucket policies accept only the principal "*", so step 4 is the only way an
// anonymous request is allowed: a statement of the bucket policy must allow
// it explicitly.
package bucketpolicy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// DefaultTTL is how long a node trusts a bucket policy it has read. A change
// made on this node applies at once; one made on another node applies here
// within DefaultTTL.
const DefaultTTL = time.Second

// maxCached bounds the cache. Past it the cache is emptied, which only costs
// a metadata read per bucket.
const maxCached = 4096

// Identity decides on the identity policies of a caller. *iam.Store
// implements it.
type Identity interface {
	Decide(ctx context.Context, id auth.Identity, action auth.Action, bucket, key string) (policy.Decision, error)
}

// Buckets reads bucket records. object.Layer implements it.
type Buckets interface {
	GetBucketInfo(ctx context.Context, bucket string) (object.BucketInfo, error)
}

// Authorizer implements auth.Authorizer. It is safe for concurrent use.
type Authorizer struct {
	identity Identity
	buckets  Buckets
	ttl      time.Duration
	now      func() time.Time
	logger   *slog.Logger

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	pol     *policy.Policy // nil when the bucket has no usable policy
	expires time.Time
}

// New returns an Authorizer. A ttl of zero selects DefaultTTL; a nil logger
// uses slog.Default.
func New(identity Identity, buckets Buckets, ttl time.Duration, logger *slog.Logger) *Authorizer {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Authorizer{identity: identity, buckets: buckets, ttl: ttl, now: time.Now, logger: logger, cache: make(map[string]cached)}
}

var _ auth.Authorizer = (*Authorizer)(nil)

// Authorize implements auth.Authorizer.
func (a *Authorizer) Authorize(ctx context.Context, id auth.Identity, action auth.Action, bucket, key string) error {
	if id.Root {
		return nil
	}
	bucketDecision := policy.Implicit
	if bucket != "" {
		pol, err := a.policy(ctx, bucket)
		if err != nil {
			return err
		}
		bucketDecision = pol.Evaluate(action, iam.ResourceARN(bucket, key))
		if bucketDecision == policy.Deny {
			return denied()
		}
	}
	identityDecision, err := a.identity.Decide(ctx, id, action, bucket, key)
	if err != nil {
		return err
	}
	switch {
	case identityDecision == policy.Deny:
		return denied()
	case identityDecision == policy.Allow, bucketDecision == policy.Allow:
		return nil
	}
	return denied()
}

// Invalidate forgets the cached policy of bucket, so that a change made on
// this node applies to the next request.
func (a *Authorizer) Invalidate(bucket string) {
	a.mu.Lock()
	delete(a.cache, bucket)
	a.mu.Unlock()
}

// policy returns the parsed policy of bucket, or nil when it has none or the
// bucket does not exist. A stored document that no longer parses is logged
// and treated as absent: it can then neither allow nor deny, so anonymous
// access fails closed and signed callers fall back to their own policies.
func (a *Authorizer) policy(ctx context.Context, bucket string) (*policy.Policy, error) {
	now := a.now()
	a.mu.Lock()
	c, ok := a.cache[bucket]
	a.mu.Unlock()
	if ok && now.Before(c.expires) {
		return c.pol, nil
	}
	info, err := a.buckets.GetBucketInfo(ctx, bucket)
	var pol *policy.Policy
	switch {
	case err == nil && len(info.Policy) > 0:
		pol, err = policy.ParseBucket(info.Policy, bucket)
		if err != nil {
			a.logger.Error("stored bucket policy does not parse; treating it as absent", "bucket", bucket, "err", err)
			pol, err = nil, nil
		}
	case err == nil:
	case isNoBucket(err), s3err.CodeOf(err) == s3err.InvalidBucketName:
		// Nothing to apply. The handler reports the missing bucket to a
		// caller that may see it; an anonymous caller is refused first.
		err = nil
	default:
		return nil, err
	}
	a.mu.Lock()
	if len(a.cache) >= maxCached {
		clear(a.cache)
	}
	a.cache[bucket] = cached{pol: pol, expires: now.Add(a.ttl)}
	a.mu.Unlock()
	return pol, nil
}

func isNoBucket(err error) bool {
	return s3err.CodeOf(err) == s3err.NoSuchBucket
}

func denied() error {
	return s3err.Errorf(s3err.AccessDenied, "Access Denied")
}
