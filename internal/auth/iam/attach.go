package iam

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eukalpia/unio/internal/meta"
)

// maxAttachedPolicies bounds the managed policies attached to one user. It is
// the AWS default.
const maxAttachedPolicies = 10

// attachKey identifies one incarnation of a managed policy.
type attachKey struct {
	name string
	id   string
}

// AttachUserPolicy attaches the managed policy name to the user. Attaching a
// policy that the user already has succeeds and changes nothing. A user may
// have at most maxAttachedPolicies policies attached, and an attach beyond
// that fails with ErrAttachmentQuota.
//
// It fails with ErrUserNotFound for a missing user, and with ErrPolicyNotFound
// for a missing policy or one whose deletion is in progress. The root identity
// has full access and cannot have policies, so ErrRootPolicy is returned.
//
// The attachment is written to the user record first, and the policy is
// confirmed after the write. If the policy is gone, or is being deleted, the
// attachment is removed again. See DeletePolicy for why the order matters.
//
// Local caches are cleared at once, and other nodes see the change within
// CacheTTL.
func (s *Store) AttachUserPolicy(ctx context.Context, user, name string) error {
	if user == s.rootKey {
		return ErrRootPolicy
	}
	owner, err := s.requireUser(ctx, user)
	if err != nil {
		return err
	}
	pol, err := s.readPolicy(ctx, name)
	if err != nil {
		return err
	}
	wrote := false
	err = s.mutateUser(ctx, user, owner.ID, func(rec *userRecord) (bool, error) {
		changed, err := addAttachment(rec, name, pol.ID)
		wrote = changed
		return changed, err
	})
	s.users.remove(user)
	if err != nil {
		return err
	}
	cur, cerr := s.readPolicy(ctx, name)
	if cerr == nil && cur.ID == pol.ID {
		return nil
	}
	if wrote {
		if err := s.dropAttachment(ctx, user, owner.ID, name, pol.ID); err != nil {
			return err
		}
	}
	if cerr != nil && !errors.Is(cerr, ErrPolicyNotFound) {
		return cerr
	}
	return ErrPolicyNotFound
}

// DetachUserPolicy detaches the managed policy name from the user. It fails
// with ErrPolicyNotAttached when the user does not have the policy attached,
// and with ErrUserNotFound for a missing user. The root identity cannot have
// policies, so ErrRootPolicy is returned. An attachment of a policy that no
// longer exists can still be detached, which clears it.
//
// Local caches are cleared at once, and other nodes see the change within
// CacheTTL.
func (s *Store) DetachUserPolicy(ctx context.Context, user, name string) error {
	if user == s.rootKey {
		return ErrRootPolicy
	}
	owner, err := s.requireUser(ctx, user)
	if err != nil {
		return err
	}
	found := false
	err = s.mutateUser(ctx, user, owner.ID, func(rec *userRecord) (bool, error) {
		i, ok := findAttachment(rec.Attached, name)
		found = ok
		if !ok {
			return false, nil
		}
		rec.Attached = slices.Delete(rec.Attached, i, i+1)
		return true, nil
	})
	s.users.remove(user)
	if err != nil {
		return err
	}
	if !found {
		return ErrPolicyNotAttached
	}
	return nil
}

// ListUserPolicies returns the names of the managed policies that apply to the
// user, sorted by name. An attachment whose policy is missing, being deleted,
// or of another incarnation is left out, because it grants nothing.
func (s *Store) ListUserPolicies(ctx context.Context, user string) ([]string, error) {
	owner, err := s.requireUser(ctx, user)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, a := range owner.Attached {
		cur, err := s.readPolicy(ctx, a.Name)
		if errors.Is(err, ErrPolicyNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if cur.ID == a.PolicyID {
			out = append(out, a.Name)
		}
	}
	return out, nil
}

// mutateUser applies fn to the record of the user incarnation id with a
// compare-and-swap, retrying on conflicts. It fails with ErrUserNotFound when
// the user is absent, is being deleted, or belongs to another incarnation. fn
// reports whether it changed the record, and an unchanged record is not
// written.
func (s *Store) mutateUser(ctx context.Context, name, id string, fn func(rec *userRecord) (bool, error)) error {
	for range s.maxRetries {
		raw, rev, err := s.kv.Get(ctx, userPath(name))
		if errors.Is(err, meta.ErrNotFound) {
			return ErrUserNotFound
		}
		if err != nil {
			return fmt.Errorf("iam: read user: %w", err)
		}
		rec, err := decodeUserRecord(raw)
		if err != nil {
			return fmt.Errorf("iam: corrupt user record: %w", err)
		}
		if rec.Deleting || rec.ID != id {
			return ErrUserNotFound
		}
		changed, err := fn(&rec)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		next, err := encodeRecord(rec)
		if err != nil {
			return fmt.Errorf("iam: encode user: %w", err)
		}
		_, err = s.kv.Put(ctx, userPath(name), next, rev)
		if err == nil {
			return nil
		}
		if !errors.Is(err, meta.ErrRevisionMismatch) {
			return fmt.Errorf("iam: store user: %w", err)
		}
	}
	return ErrTooManyRetries
}

// dropAttachment removes the attachment of policy name with incarnation
// policyID from the user incarnation id. It is the rollback of an attach whose
// policy turned out to be gone. An attachment that is no longer there, or is of
// another incarnation, is left alone. A user that has gone meanwhile has
// nothing to roll back.
func (s *Store) dropAttachment(ctx context.Context, user, id, policy, policyID string) error {
	err := s.mutateUser(ctx, user, id, func(rec *userRecord) (bool, error) {
		i, ok := findAttachment(rec.Attached, policy)
		if !ok || rec.Attached[i].PolicyID != policyID {
			return false, nil
		}
		rec.Attached = slices.Delete(rec.Attached, i, i+1)
		return true, nil
	})
	s.users.remove(user)
	if errors.Is(err, ErrUserNotFound) {
		return nil
	}
	return err
}

// addAttachment adds the attachment of policy name with incarnation id to the
// record and reports whether the record changed. An attachment to an earlier
// incarnation of the name is replaced, and it does not count again.
// Otherwise the new attachment is refused when the user already has
// maxAttachedPolicies.
func addAttachment(rec *userRecord, name, id string) (bool, error) {
	i, found := findAttachment(rec.Attached, name)
	if found {
		if rec.Attached[i].PolicyID == id {
			return false, nil
		}
		rec.Attached[i].PolicyID = id
		return true, nil
	}
	if len(rec.Attached) >= maxAttachedPolicies {
		return false, ErrAttachmentQuota
	}
	rec.Attached = slices.Insert(rec.Attached, i, attachment{Name: name, PolicyID: id})
	return true, nil
}

// findAttachment returns the position of name in list, which is sorted by name,
// and whether it is there. When it is not, the position is where it belongs.
func findAttachment(list []attachment, name string) (int, bool) {
	return slices.BinarySearchFunc(list, name, func(a attachment, n string) int {
		return strings.Compare(a.Name, n)
	})
}

// attachmentCounts returns, for each incarnation of a managed policy, the
// number of users that have it attached. Users whose deletion is in progress
// are not counted, since evaluation treats them as absent.
func (s *Store) attachmentCounts(ctx context.Context) (map[attachKey]int, error) {
	kvs, err := s.kv.List(ctx, usersPrefix)
	if err != nil {
		return nil, fmt.Errorf("iam: list users: %w", err)
	}
	counts := make(map[attachKey]int)
	for _, kv := range kvs {
		rec, err := decodeUserRecord(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt user record: %w", err)
		}
		if rec.Deleting {
			continue
		}
		for _, a := range rec.Attached {
			counts[attachKey{name: a.Name, id: a.PolicyID}]++
		}
	}
	return counts, nil
}
