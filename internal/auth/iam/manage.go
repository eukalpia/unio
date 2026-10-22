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

// CreateUser creates an enabled user. It fails with ErrUserExists if the
// name is taken.
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
	// Expecting revision 0 makes the create fail if the name is taken.
	_, err = s.kv.Put(ctx, userPath(name), raw, 0)
	if errors.Is(err, meta.ErrRevisionMismatch) {
		return auth.UserInfo{}, ErrUserExists
	}
	if err != nil {
		return auth.UserInfo{}, fmt.Errorf("iam: create user: %w", err)
	}
	s.users.remove(name)
	return auth.UserInfo{Name: name, Created: now, Enabled: true}, nil
}

// DeleteUser removes a user, its access keys and its policy. The user record
// goes first, so none of the keys can authenticate once it is gone. If a
// later step fails, the leftovers are inert, and a repeat call removes them.
// A repeat call on a user that is already gone still cleans up, and it
// returns ErrUserNotFound.
func (s *Store) DeleteUser(ctx context.Context, name string) error {
	if !validUserName(name) {
		return ErrUserNotFound
	}
	found := true
	if err := s.deleteKey(ctx, userPath(name)); err != nil {
		if !errors.Is(err, meta.ErrNotFound) {
			return fmt.Errorf("iam: delete user: %w", err)
		}
		found = false
	}
	s.clearCaches()
	if err := s.deleteUserKeys(ctx, name); err != nil {
		return err
	}
	if err := s.kv.Delete(ctx, policyPath(name), meta.AnyRevision); err != nil {
		return fmt.Errorf("iam: delete user policy: %w", err)
	}
	s.clearCaches()
	if !found {
		return ErrUserNotFound
	}
	return nil
}

// deleteUserKeys removes every access key record that names the user.
func (s *Store) deleteUserKeys(ctx context.Context, name string) error {
	kvs, err := s.kv.List(ctx, keysPrefix)
	if err != nil {
		return fmt.Errorf("iam: list access keys: %w", err)
	}
	for _, kv := range kvs {
		rec, err := decodeKeyRecord(kv.Value)
		if err != nil || rec.User != name {
			continue
		}
		if err := s.kv.Delete(ctx, kv.Key, meta.AnyRevision); err != nil {
			return fmt.Errorf("iam: delete access key: %w", err)
		}
	}
	return nil
}

// ListUsers returns every user, sorted by name.
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
		out = append(out, auth.UserInfo{Name: rec.Name, Created: rec.Created, Enabled: rec.Enabled})
	}
	return out, nil
}

// CreateAccessKey generates an access key for user. The secret is returned
// only here. Stored secrets are sealed, and nothing can recover the secret
// later. A collision on the random key ID is retried.
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
		s.keys.remove(id)
		return auth.AccessKey{ID: id, Secret: secret, User: user, Created: now}, nil
	}
	return auth.AccessKey{}, ErrTooManyRetries
}

// DeleteAccessKey removes an access key. Its cached entry is dropped at once.
func (s *Store) DeleteAccessKey(ctx context.Context, accessKeyID string) error {
	if !validAccessKeyID(accessKeyID) {
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
	s.users.remove(user)
	return nil
}

// SetUserEnabled enables or disables a user. A disabled user's keys stop
// authenticating and the user is denied everything. This is not part of
// auth.IAM, and it is the only way to disable a user.
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
		rec.Enabled = enabled
		return encodeRecord(rec)
	})
	if err != nil {
		return err
	}
	s.clearCaches()
	return nil
}

// requireUser returns the stored user record, or ErrUserNotFound.
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
	return rec, nil
}
