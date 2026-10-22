package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/policy"
	"github.com/eukalpia/unio/internal/meta"
)

// CreateUser creates an enabled user. It fails with ErrUserExists if the name
// is taken, and with ErrUserDeleting while a deletion of that name is in
// progress.
func (s *Store) CreateUser(ctx context.Context, name string) (auth.UserInfo, error) {
	if !validUserName(name) {
		return auth.UserInfo{}, ErrInvalidUserName
	}
	id, err := newUserID()
	if err != nil {
		return auth.UserInfo{}, fmt.Errorf("iam: generate user id: %w", err)
	}
	now := s.clock().UTC().Round(0)
	raw, err := encodeRecord(userRecord{V: recordVersion, ID: id, Name: name, Created: now, Enabled: true})
	if err != nil {
		return auth.UserInfo{}, fmt.Errorf("iam: encode user: %w", err)
	}
	for range s.maxRetries {
		// Expecting revision 0 makes the create fail if the name is taken.
		_, err = s.kv.Put(ctx, userPath(name), raw, 0)
		if err == nil {
			s.users.remove(name)
			return auth.UserInfo{Name: name, Created: now, Enabled: true}, nil
		}
		if !errors.Is(err, meta.ErrRevisionMismatch) {
			return auth.UserInfo{}, fmt.Errorf("iam: create user: %w", err)
		}
		// The name is taken. Report whether the holder is being deleted.
		cur, _, err := s.kv.Get(ctx, userPath(name))
		if errors.Is(err, meta.ErrNotFound) {
			continue
		}
		if err != nil {
			return auth.UserInfo{}, fmt.Errorf("iam: read user: %w", err)
		}
		rec, err := decodeUserRecord(cur)
		if err != nil {
			return auth.UserInfo{}, fmt.Errorf("iam: corrupt user record: %w", err)
		}
		if rec.Deleting {
			return auth.UserInfo{}, ErrUserDeleting
		}
		return auth.UserInfo{}, ErrUserExists
	}
	return auth.UserInfo{}, ErrTooManyRetries
}

// DeleteUser removes a user, its access keys and its policy.
//
// The deletion has three steps. First, the user record is marked deleting and
// disabled in one compare-and-swap, so the user stops authenticating at once,
// and the name cannot be reused until the record is removed. Second, the
// keys and the policy that belong to this incarnation are deleted. Third, the
// record is removed. A failure after the first step leaves a deleting record,
// and a repeat call finishes the job and returns nil.
func (s *Store) DeleteUser(ctx context.Context, name string) error {
	if !validUserName(name) {
		return ErrUserNotFound
	}
	owner, err := s.markDeleting(ctx, name)
	if err != nil {
		return err
	}
	s.clearCaches()
	if err := s.deleteIncarnation(ctx, name, owner.ID); err != nil {
		return err
	}
	if err := s.deleteKey(ctx, userPath(name)); err != nil && !errors.Is(err, meta.ErrNotFound) {
		return fmt.Errorf("iam: remove user: %w", err)
	}
	s.clearCaches()
	return nil
}

// markDeleting turns the user record into a deleting record in one
// compare-and-swap. It returns the record of the incarnation being deleted,
// or ErrUserNotFound when there is no user with that name.
func (s *Store) markDeleting(ctx context.Context, name string) (userRecord, error) {
	var owner userRecord
	err := s.update(ctx, userPath(name), func(cur []byte, exists bool) ([]byte, error) {
		if !exists {
			return nil, ErrUserNotFound
		}
		rec, err := decodeUserRecord(cur)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt user record: %w", err)
		}
		rec.Deleting = true
		rec.Enabled = false
		owner = rec
		return encodeRecord(rec)
	})
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return userRecord{}, err
		}
		return userRecord{}, fmt.Errorf("iam: delete user: %w", err)
	}
	return owner, nil
}

// deleteIncarnation deletes the access keys and the policy that belong to the
// user incarnation with the given id. Records of any other incarnation of the
// name are left alone, so the cleanup can never remove a user created later
// under the same name.
func (s *Store) deleteIncarnation(ctx context.Context, name, id string) error {
	kvs, err := s.kv.List(ctx, keysPrefix)
	if err != nil {
		return fmt.Errorf("iam: list access keys: %w", err)
	}
	for _, kv := range kvs {
		rec, err := decodeKeyRecord(kv.Value)
		if err != nil || rec.User != name || rec.UserID != id {
			continue
		}
		if err := s.kv.Delete(ctx, kv.Key, meta.AnyRevision); err != nil && !errors.Is(err, meta.ErrNotFound) {
			return fmt.Errorf("iam: delete access key: %w", err)
		}
	}
	raw, _, err := s.kv.Get(ctx, policyPath(name))
	if errors.Is(err, meta.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("iam: read user policy: %w", err)
	}
	if rec, err := decodePolicyRecord(raw); err == nil && rec.UserID != id {
		return nil
	}
	if err := s.kv.Delete(ctx, policyPath(name), meta.AnyRevision); err != nil && !errors.Is(err, meta.ErrNotFound) {
		return fmt.Errorf("iam: delete user policy: %w", err)
	}
	return nil
}

// ListUsers returns every user, sorted by name. Users whose deletion is in
// progress are not listed.
func (s *Store) ListUsers(ctx context.Context) ([]auth.UserInfo, error) {
	kvs, err := s.kv.List(ctx, usersPrefix)
	if err != nil {
		return nil, fmt.Errorf("iam: list users: %w", err)
	}
	out := make([]auth.UserInfo, 0, len(kvs))
	for _, kv := range kvs {
		rec, err := decodeUserRecord(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt user record: %w", err)
		}
		if rec.Deleting {
			continue
		}
		out = append(out, auth.UserInfo{Name: rec.Name, Created: rec.Created, Enabled: rec.Enabled})
	}
	return out, nil
}

// CreateAccessKey generates an access key for user. The secret is returned
// only here. Stored secrets are sealed, and nothing can recover the secret
// later. A collision on the random key ID is retried.
//
// The owner is checked again after the key is written. If the user was deleted
// meanwhile, the key is removed and the call fails, so no key outlives its
// user.
func (s *Store) CreateAccessKey(ctx context.Context, user string) (auth.AccessKey, error) {
	owner, err := s.requireUser(ctx, user)
	if err != nil {
		return auth.AccessKey{}, err
	}
	for range s.maxRetries {
		id, err := newAccessKeyID()
		if err != nil {
			return auth.AccessKey{}, fmt.Errorf("iam: generate access key: %w", err)
		}
		if id == s.rootKey {
			continue
		}
		secret, err := newSecret()
		if err != nil {
			return auth.AccessKey{}, fmt.Errorf("iam: generate secret: %w", err)
		}
		sealed, err := seal(s.aead, []byte(id), []byte(secret))
		if err != nil {
			return auth.AccessKey{}, fmt.Errorf("iam: seal secret: %w", err)
		}
		now := s.clock().UTC().Round(0)
		raw, err := encodeRecord(keyRecord{
			V: recordVersion, User: user, UserID: owner.ID, Created: now, Secret: sealed,
		})
		if err != nil {
			return auth.AccessKey{}, fmt.Errorf("iam: encode access key: %w", err)
		}
		_, err = s.kv.Put(ctx, keyPath(id), raw, 0)
		if errors.Is(err, meta.ErrRevisionMismatch) {
			continue
		}
		if err != nil {
			return auth.AccessKey{}, fmt.Errorf("iam: store access key: %w", err)
		}
		if err := s.confirmOwner(ctx, user, owner.ID); err != nil {
			if derr := s.kv.Delete(ctx, keyPath(id), meta.AnyRevision); derr != nil {
				return auth.AccessKey{}, fmt.Errorf("iam: remove key of a deleted user: %w", derr)
			}
			return auth.AccessKey{}, err
		}
		s.keys.remove(id)
		return auth.AccessKey{ID: id, Secret: secret, User: user, Created: now}, nil
	}
	return auth.AccessKey{}, ErrTooManyRetries
}

// DeleteAccessKey removes an access key. Its cached entry is dropped at once.
func (s *Store) DeleteAccessKey(ctx context.Context, accessKeyID string) error {
	if !auth.ValidAccessKeyID(accessKeyID) {
		return ErrAccessKeyNotFound
	}
	if err := s.deleteKey(ctx, keyPath(accessKeyID)); err != nil {
		if errors.Is(err, meta.ErrNotFound) {
			return ErrAccessKeyNotFound
		}
		return fmt.Errorf("iam: delete access key: %w", err)
	}
	s.keys.remove(accessKeyID)
	return nil
}

// ListAccessKeys returns the access keys of user, without secrets.
func (s *Store) ListAccessKeys(ctx context.Context, user string) ([]auth.AccessKey, error) {
	owner, err := s.requireUser(ctx, user)
	if err != nil {
		return nil, err
	}
	kvs, err := s.kv.List(ctx, keysPrefix)
	if err != nil {
		return nil, fmt.Errorf("iam: list access keys: %w", err)
	}
	var out []auth.AccessKey
	for _, kv := range kvs {
		rec, err := decodeKeyRecord(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt access key record: %w", err)
		}
		if rec.User != user || rec.UserID != owner.ID {
			continue
		}
		out = append(out, auth.AccessKey{
			ID:      strings.TrimPrefix(kv.Key, keysPrefix),
			User:    user,
			Created: rec.Created,
		})
	}
	return out, nil
}

// SetUserPolicy replaces the user's policy with doc, after validating it. An
// empty document detaches the policy.
//
// As in CreateAccessKey, the owner is checked after the write, and the policy
// is removed if the user was deleted meanwhile.
func (s *Store) SetUserPolicy(ctx context.Context, user string, doc []byte) error {
	owner, err := s.requireUser(ctx, user)
	if err != nil {
		return err
	}
	if len(doc) == 0 {
		if err := s.kv.Delete(ctx, policyPath(user), meta.AnyRevision); err != nil {
			return fmt.Errorf("iam: detach policy: %w", err)
		}
		s.users.remove(user)
		return nil
	}
	if _, err := policy.Parse(doc); err != nil {
		return err
	}
	raw, err := encodeRecord(policyRecord{V: recordVersion, UserID: owner.ID, Document: string(doc)})
	if err != nil {
		return fmt.Errorf("iam: encode policy: %w", err)
	}
	err = s.update(ctx, policyPath(user), func([]byte, bool) ([]byte, error) { return raw, nil })
	if err != nil {
		return fmt.Errorf("iam: store policy: %w", err)
	}
	if err := s.confirmOwner(ctx, user, owner.ID); err != nil {
		if derr := s.kv.Delete(ctx, policyPath(user), meta.AnyRevision); derr != nil {
			return fmt.Errorf("iam: remove policy of a deleted user: %w", derr)
		}
		return err
	}
	s.users.remove(user)
	return nil
}

// SetUserEnabled enables or disables a user. A disabled user's keys stop
// authenticating and the user is denied everything. Local caches are cleared
// at once; other nodes see the change within CacheTTL. A user whose deletion
// is in progress is treated as absent.
func (s *Store) SetUserEnabled(ctx context.Context, user string, enabled bool) error {
	if !validUserName(user) {
		return ErrUserNotFound
	}
	err := s.update(ctx, userPath(user), func(cur []byte, exists bool) ([]byte, error) {
		if !exists {
			return nil, ErrUserNotFound
		}
		rec, err := decodeUserRecord(cur)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt user record: %w", err)
		}
		if rec.Deleting {
			return nil, ErrUserNotFound
		}
		rec.Enabled = enabled
		return encodeRecord(rec)
	})
	if err != nil {
		return err
	}
	s.clearCaches()
	return nil
}

// requireUser returns the stored record of an active user, or ErrUserNotFound
// when there is no such user or its deletion is in progress.
func (s *Store) requireUser(ctx context.Context, name string) (userRecord, error) {
	if !validUserName(name) {
		return userRecord{}, ErrUserNotFound
	}
	raw, _, err := s.kv.Get(ctx, userPath(name))
	if errors.Is(err, meta.ErrNotFound) {
		return userRecord{}, ErrUserNotFound
	}
	if err != nil {
		return userRecord{}, fmt.Errorf("iam: read user: %w", err)
	}
	rec, err := decodeUserRecord(raw)
	if err != nil {
		return userRecord{}, fmt.Errorf("iam: corrupt user record: %w", err)
	}
	if rec.Deleting {
		return userRecord{}, ErrUserNotFound
	}
	return rec, nil
}

// confirmOwner checks that the user name still belongs to the incarnation id.
// Writes that depend on a user call it after they write, which closes the
// window in which a concurrent deletion could miss the write.
func (s *Store) confirmOwner(ctx context.Context, name, id string) error {
	rec, err := s.requireUser(ctx, name)
	if err != nil {
		return err
	}
	if rec.ID != id {
		return ErrUserNotFound
	}
	return nil
}
