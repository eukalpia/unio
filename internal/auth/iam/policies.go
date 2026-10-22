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

// maxPolicyNameLen bounds the length of a managed policy name.
const maxPolicyNameLen = 128

// maxManagedPolicies bounds the number of managed policies. It is the AWS
// default.
const maxManagedPolicies = 1000

// validPolicyName reports whether name is an acceptable managed policy name:
// 1 to 128 characters, each an ASCII letter, a digit, or one of +=,.@_-. These
// are the characters AWS accepts. The name becomes part of a SystemKV key, so
// '/' and other separators are excluded.
func validPolicyName(name string) bool {
	if len(name) == 0 || len(name) > maxPolicyNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isPolicyNameChar(name[i]) {
			return false
		}
	}
	return true
}

// isPolicyNameChar reports whether c may appear in a managed policy name.
func isPolicyNameChar(c byte) bool {
	switch c {
	case '+', '=', ',', '.', '@', '_', '-':
		return true
	}
	return isAlnum(c)
}

// PutPolicy creates the managed policy name with document, or replaces the
// document of a policy that exists. The document is validated with
// policy.Parse, and nothing is stored when it is invalid.
//
// A replacement keeps the incarnation of the policy, so its attachments stay
// in place. Creating a policy takes one of the maxManagedPolicies slots, and
// fails with ErrPolicyQuota when none is free. A policy whose deletion is in
// progress cannot be replaced: PutPolicy fails with ErrPolicyDeleting until
// the deletion has ended.
//
// Local caches are cleared at once, and other nodes see the change within
// CacheTTL.
func (s *Store) PutPolicy(ctx context.Context, name string, doc []byte) error {
	if !validPolicyName(name) {
		return ErrInvalidPolicyName
	}
	if _, err := policy.Parse(doc); err != nil {
		return err
	}
	for range s.maxRetries {
		raw, rev, err := s.kv.Get(ctx, managedPath(name))
		if errors.Is(err, meta.ErrNotFound) {
			created, err := s.createPolicy(ctx, name, doc)
			if err != nil {
				return err
			}
			if created {
				s.clearCaches()
				return nil
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("iam: read policy: %w", err)
		}
		rec, err := decodeManagedRecord(raw)
		if err != nil {
			return fmt.Errorf("iam: corrupt policy record: %w", err)
		}
		if rec.Deleting {
			return ErrPolicyDeleting
		}
		next, err := encodeRecord(managedRecord{V: recordVersion, ID: rec.ID, Document: string(doc)})
		if err != nil {
			return fmt.Errorf("iam: encode policy: %w", err)
		}
		_, err = s.kv.Put(ctx, managedPath(name), next, rev)
		if err == nil {
			s.clearCaches()
			return nil
		}
		if !errors.Is(err, meta.ErrRevisionMismatch) {
			return fmt.Errorf("iam: store policy: %w", err)
		}
	}
	return ErrTooManyRetries
}

// createPolicy creates the record of a policy that does not exist. It takes a
// slot first. The slot is given back only when the record is known not to
// have been stored. It reports false, without an error, when a concurrent
// writer created the name first, so the caller retries as a replacement.
func (s *Store) createPolicy(ctx context.Context, name string, doc []byte) (bool, error) {
	id, err := newIncarnationID()
	if err != nil {
		return false, fmt.Errorf("iam: generate policy id: %w", err)
	}
	raw, err := encodeRecord(managedRecord{V: recordVersion, ID: id, Document: string(doc)})
	if err != nil {
		return false, fmt.Errorf("iam: encode policy: %w", err)
	}
	if err := s.takeSlot(ctx); err != nil {
		return false, err
	}
	// Expecting revision 0 makes the create fail if the name is taken.
	_, err = s.kv.Put(ctx, managedPath(name), raw, 0)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, meta.ErrRevisionMismatch) {
		// The write may have reached the store. Keep the slot, which errs
		// towards refusing.
		return false, fmt.Errorf("iam: create policy: %w", err)
	}
	if err := s.giveSlot(ctx); err != nil {
		return false, err
	}
	return false, nil
}

// GetPolicy returns a managed policy with the number of users it is attached
// to. It fails with ErrPolicyNotFound for a missing policy and for one whose
// deletion is in progress.
func (s *Store) GetPolicy(ctx context.Context, name string) (auth.Policy, error) {
	rec, err := s.readPolicy(ctx, name)
	if err != nil {
		return auth.Policy{}, err
	}
	counts, err := s.attachmentCounts(ctx)
	if err != nil {
		return auth.Policy{}, err
	}
	return auth.Policy{
		Name:        name,
		Document:    []byte(rec.Document),
		Attachments: counts[attachKey{name: name, id: rec.ID}],
	}, nil
}

// ListPolicies returns every managed policy, sorted by name, with its
// attachment count. Policies whose deletion is in progress are not listed.
func (s *Store) ListPolicies(ctx context.Context) ([]auth.Policy, error) {
	kvs, err := s.kv.List(ctx, managedPrefix)
	if err != nil {
		return nil, fmt.Errorf("iam: list policies: %w", err)
	}
	counts, err := s.attachmentCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]auth.Policy, 0, len(kvs))
	for _, kv := range kvs {
		rec, err := decodeManagedRecord(kv.Value)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt policy record: %w", err)
		}
		if rec.Deleting {
			continue
		}
		name := strings.TrimPrefix(kv.Key, managedPrefix)
		out = append(out, auth.Policy{
			Name:        name,
			Document:    []byte(rec.Document),
			Attachments: counts[attachKey{name: name, id: rec.ID}],
		})
	}
	return out, nil
}

// DeletePolicy deletes a managed policy. A policy that is still attached to a
// user cannot be deleted: DeletePolicy fails with ErrPolicyInUse until every
// attachment has been detached.
//
// The deletion has three steps, as DeleteUser does. First the record is marked
// as deleting. The policy then grants nothing, and new attachments to it are
// refused. Second, the attachments are counted. If there are none, the record
// is removed and its slot is given back. If there are some, the mark is undone
// and the deletion is refused.
//
// The order keeps an attachment from outliving its policy. An attach writes
// the attachment first, then confirms that the policy is live. If the confirm
// sees the mark, the attach removes its own attachment again. If it does not,
// the write came before the mark and the count below sees it. Evaluation
// ignores attachments of missing or deleting policies in any case, so even an
// attachment that a failed rollback left behind grants nothing.
func (s *Store) DeletePolicy(ctx context.Context, name string) error {
	id, err := s.markPolicyDeleting(ctx, name)
	if err != nil {
		return err
	}
	s.clearCaches()
	counts, err := s.attachmentCounts(ctx)
	if err != nil {
		return s.undoPolicyDelete(ctx, name, id, err)
	}
	if counts[attachKey{name: name, id: id}] > 0 {
		return s.undoPolicyDelete(ctx, name, id, ErrPolicyInUse)
	}
	removed, err := s.deleteWhile(ctx, managedPath(name), ownedManaged(id))
	if err != nil {
		return fmt.Errorf("iam: delete policy: %w", err)
	}
	s.clearCaches()
	if !removed {
		// A concurrent deletion removed this incarnation first, and gave its
		// slot back.
		return ErrPolicyNotFound
	}
	return s.giveSlot(ctx)
}

// undoPolicyDelete ends a deletion that was refused, so the policy is live
// again, and returns cause. If the mark cannot be undone, the policy stays
// marked. That is safe, since a marked policy grants nothing, and a repeat
// DeletePolicy finishes the job.
func (s *Store) undoPolicyDelete(ctx context.Context, name, id string, cause error) error {
	err := s.unmarkPolicy(ctx, name, id)
	s.clearCaches()
	if err != nil {
		return fmt.Errorf("iam: restore policy after refused delete: %w", err)
	}
	return cause
}

// readPolicy returns the live record of a managed policy. It fails with
// ErrPolicyNotFound when the name is invalid, the policy is missing, or its
// deletion is in progress.
func (s *Store) readPolicy(ctx context.Context, name string) (managedRecord, error) {
	if !validPolicyName(name) {
		return managedRecord{}, ErrPolicyNotFound
	}
	raw, _, err := s.kv.Get(ctx, managedPath(name))
	if errors.Is(err, meta.ErrNotFound) {
		return managedRecord{}, ErrPolicyNotFound
	}
	if err != nil {
		return managedRecord{}, fmt.Errorf("iam: read policy: %w", err)
	}
	rec, err := decodeManagedRecord(raw)
	if err != nil {
		return managedRecord{}, fmt.Errorf("iam: corrupt policy record: %w", err)
	}
	if rec.Deleting {
		return managedRecord{}, ErrPolicyNotFound
	}
	return rec, nil
}

// markPolicyDeleting marks a managed policy as being deleted and returns its
// incarnation id. It fails with ErrPolicyNotFound when the policy does not
// exist. A policy that is already marked is marked again, which finishes an
// interrupted deletion.
func (s *Store) markPolicyDeleting(ctx context.Context, name string) (string, error) {
	if !validPolicyName(name) {
		return "", ErrPolicyNotFound
	}
	var id string
	err := s.update(ctx, managedPath(name), func(cur []byte, exists bool) ([]byte, error) {
		if !exists {
			return nil, ErrPolicyNotFound
		}
		rec, err := decodeManagedRecord(cur)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt policy record: %w", err)
		}
		rec.Deleting = true
		id = rec.ID
		return encodeRecord(rec)
	})
	if err != nil {
		if errors.Is(err, ErrPolicyNotFound) {
			return "", err
		}
		return "", fmt.Errorf("iam: delete policy: %w", err)
	}
	return id, nil
}

// unmarkPolicy ends a refused deletion, so the policy is live again. It does
// nothing when the record is gone or belongs to another incarnation.
func (s *Store) unmarkPolicy(ctx context.Context, name, id string) error {
	err := s.update(ctx, managedPath(name), func(cur []byte, exists bool) ([]byte, error) {
		if !exists {
			return nil, ErrPolicyNotFound
		}
		rec, err := decodeManagedRecord(cur)
		if err != nil {
			return nil, fmt.Errorf("iam: corrupt policy record: %w", err)
		}
		if rec.ID != id {
			return nil, ErrPolicyNotFound
		}
		rec.Deleting = false
		return encodeRecord(rec)
	})
	if errors.Is(err, ErrPolicyNotFound) {
		return nil
	}
	return err
}

// takeSlot takes one managed policy slot, or fails with ErrPolicyQuota.
func (s *Store) takeSlot(ctx context.Context) error {
	return s.adjustQuota(ctx, func(count int) (int, error) {
		if count >= maxManagedPolicies {
			return 0, ErrPolicyQuota
		}
		return count + 1, nil
	})
}

// giveSlot gives back one managed policy slot. The count never drops below
// zero.
func (s *Store) giveSlot(ctx context.Context) error {
	return s.adjustQuota(ctx, func(count int) (int, error) {
		return max(count-1, 0), nil
	})
}

// adjustQuota applies next to the slot count with a compare-and-swap.
func (s *Store) adjustQuota(ctx context.Context, next func(count int) (int, error)) error {
	err := s.update(ctx, quotaKey, func(cur []byte, exists bool) ([]byte, error) {
		count := 0
		if exists {
			rec, err := decodeQuotaRecord(cur)
			if err != nil {
				return nil, fmt.Errorf("iam: corrupt quota record: %w", err)
			}
			count = rec.Count
		}
		n, err := next(count)
		if err != nil {
			return nil, err
		}
		return encodeRecord(quotaRecord{V: recordVersion, Count: n})
	})
	if err != nil {
		return fmt.Errorf("iam: managed policy quota: %w", err)
	}
	return nil
}
