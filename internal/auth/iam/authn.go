package iam

import (
	"context"
	"errors"
	"fmt"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/auth/sigv4"
	"github.com/eukalpia/unio/internal/meta"
)

// keyLookup is a cached access key resolution. found is false for an
// unknown key, which is cached briefly so that repeated guesses cost no
// storage reads.
type keyLookup struct {
	found bool
	entry keyEntry
}

// keyEntry is a resolved access key.
type keyEntry struct {
	secret string
	user   string
}

// userLookup is a cached user resolution.
type userLookup struct {
	found bool
	entry userEntry
}

// userEntry is a user with the policies that apply to it: the inline policy
// first, then each attached managed policy that is live. pols is empty when
// the user has none.
type userEntry struct {
	id      string
	enabled bool
	pols    []*policy.Policy
}

// Lookup implements sigv4.CredentialStore. It returns the secret for an
// access key that belongs to an enabled user, or sigv4.ErrUnknownAccessKey.
// Storage and decryption failures are returned as errors with no
// access-key detail.
func (s *Store) Lookup(ctx context.Context, accessKey string) (string, auth.Identity, error) {
	if accessKey == s.rootKey {
		return s.rootSecret, auth.Identity{AccessKey: accessKey, Root: true}, nil
	}
	if !auth.ValidAccessKeyID(accessKey) {
		return "", auth.Identity{}, sigv4.ErrUnknownAccessKey
	}
	kl, err := s.keyLookup(ctx, accessKey)
	if err != nil {
		return "", auth.Identity{}, err
	}
	if !kl.found {
		return "", auth.Identity{}, sigv4.ErrUnknownAccessKey
	}
	return kl.entry.secret, auth.Identity{AccessKey: accessKey, User: kl.entry.user}, nil
}

// Authenticate verifies a request's SigV4 signature against the stored keys.
func (s *Store) Authenticate(ctx context.Context, req *auth.Request) (*auth.Result, error) {
	return s.authn.Authenticate(ctx, req)
}

// keyLookup resolves an access key, from the cache when possible. The epoch
// is taken before the load, so an invalidation that completes during the load
// keeps the loaded value out of the cache.
func (s *Store) keyLookup(ctx context.Context, accessKey string) (keyLookup, error) {
	if kl, ok := s.keys.get(accessKey); ok {
		return kl, nil
	}
	epoch := s.keys.epoch()
	kl, err := s.loadKey(ctx, accessKey)
	if err != nil {
		return keyLookup{}, err
	}
	s.keys.putAt(accessKey, kl, epoch)
	return kl, nil
}

// loadKey reads and decrypts an access key from storage. A key whose owner
// is missing, disabled, or of a different incarnation is reported as not
// found.
func (s *Store) loadKey(ctx context.Context, accessKey string) (keyLookup, error) {
	raw, _, err := s.kv.Get(ctx, keyPath(accessKey))
	if errors.Is(err, meta.ErrNotFound) {
		return keyLookup{}, nil
	}
	if err != nil {
		return keyLookup{}, fmt.Errorf("iam: read access key: %w", err)
	}
	rec, err := decodeKeyRecord(raw)
	if err != nil {
		return keyLookup{}, fmt.Errorf("iam: corrupt access key record: %w", err)
	}
	ul, err := s.userLookup(ctx, rec.User)
	if err != nil {
		return keyLookup{}, err
	}
	if !ul.found || !ul.entry.enabled || ul.entry.id != rec.UserID {
		return keyLookup{}, nil
	}
	plain, err := open(s.aead, []byte(accessKey), rec.Secret)
	if err != nil {
		return keyLookup{}, fmt.Errorf("iam: access key secret cannot be decrypted: %w", err)
	}
	secret := string(plain)
	clear(plain)
	return keyLookup{found: true, entry: keyEntry{secret: secret, user: rec.User}}, nil
}

// userLookup resolves a user and its policy, from the cache when possible. As
// in keyLookup, the epoch is taken before the load.
func (s *Store) userLookup(ctx context.Context, name string) (userLookup, error) {
	if ul, ok := s.users.get(name); ok {
		return ul, nil
	}
	epoch := s.users.epoch()
	ul, err := s.loadUser(ctx, name)
	if err != nil {
		return userLookup{}, err
	}
	s.users.putAt(name, ul, epoch)
	return ul, nil
}

// loadUser reads a user and the policies that apply to it. Policies that
// belong to an earlier user of the same name are ignored.
func (s *Store) loadUser(ctx context.Context, name string) (userLookup, error) {
	if !validUserName(name) {
		return userLookup{}, nil
	}
	raw, _, err := s.kv.Get(ctx, userPath(name))
	if errors.Is(err, meta.ErrNotFound) {
		return userLookup{}, nil
	}
	if err != nil {
		return userLookup{}, fmt.Errorf("iam: read user: %w", err)
	}
	rec, err := decodeUserRecord(raw)
	if err != nil {
		return userLookup{}, fmt.Errorf("iam: corrupt user record: %w", err)
	}
	if rec.Deleting {
		// A user being deleted is treated as absent, so it cannot authenticate
		// or be authorized while its records are removed.
		return userLookup{}, nil
	}
	pols, err := s.loadPolicies(ctx, name, rec)
	if err != nil {
		return userLookup{}, err
	}
	return userLookup{found: true, entry: userEntry{id: rec.ID, enabled: rec.Enabled, pols: pols}}, nil
}

// loadPolicies returns the policies that apply to the user incarnation rec: its
// inline policy, then every attached managed policy. A managed policy that is
// missing, being deleted, or of another incarnation grants nothing, and is
// skipped. Attachments are decided here only, so the listing and the deletion
// check in policies.go agree with evaluation.
func (s *Store) loadPolicies(ctx context.Context, name string, rec userRecord) ([]*policy.Policy, error) {
	inline, err := s.loadPolicy(ctx, name, rec.ID)
	if err != nil {
		return nil, err
	}
	var pols []*policy.Policy
	if inline != nil {
		pols = append(pols, inline)
	}
	for _, a := range rec.Attached {
		cur, err := s.readPolicy(ctx, a.Name)
		if errors.Is(err, ErrPolicyNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if cur.ID != a.PolicyID {
			continue
		}
		p, err := policy.Parse([]byte(cur.Document))
		if err != nil {
			return nil, fmt.Errorf("iam: stored policy is invalid: %w", err)
		}
		pols = append(pols, p)
	}
	return pols, nil
}

// loadPolicy reads the inline policy of a user incarnation. It returns nil when the
// user has no policy or the stored policy belongs to another incarnation.
func (s *Store) loadPolicy(ctx context.Context, name, userID string) (*policy.Policy, error) {
	raw, _, err := s.kv.Get(ctx, policyPath(name))
	if errors.Is(err, meta.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("iam: read policy: %w", err)
	}
	rec, err := decodePolicyRecord(raw)
	if err != nil {
		return nil, fmt.Errorf("iam: corrupt policy record: %w", err)
	}
	if rec.UserID != userID {
		return nil, nil
	}
	pol, err := policy.Parse([]byte(rec.Document))
	if err != nil {
		return nil, fmt.Errorf("iam: stored policy is invalid: %w", err)
	}
	return pol, nil
}
