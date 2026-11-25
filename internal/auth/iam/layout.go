package iam

import (
	"time"

	"github.com/bytedance/sonic"
)

// recordVersion is the version of every record written by this package.
// Records with another version are refused.
const recordVersion = 1

// Key prefixes and fixed keys under the SystemKV namespace.
const (
	usersPrefix    = "iam/users/"
	keysPrefix     = "iam/keys/"
	policiesPrefix = "iam/policies/"
	managedPrefix  = "iam/managed/"
	// quotaKey holds the count of managed policy slots in use.
	quotaKey = "iam/managed-count"
)

// userPath returns the SystemKV key of a user record.
func userPath(name string) string { return usersPrefix + name }

// keyPath returns the SystemKV key of an access key record.
func keyPath(accessKey string) string { return keysPrefix + accessKey }

// policyPath returns the SystemKV key of a user's inline policy record.
func policyPath(name string) string { return policiesPrefix + name }

// managedPath returns the SystemKV key of a managed policy record.
func managedPath(name string) string { return managedPrefix + name }

// userRecord is the stored form of a user.
type userRecord struct {
	V       int       `json:"v"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Enabled bool      `json:"enabled"`
	// Deleting marks a user whose deletion is in progress. Such a user is
	// disabled and treated as absent, and its name cannot be reused until
	// the record is removed.
	Deleting bool `json:"deleting"`
	// Attached lists the managed policies attached to the user, sorted by
	// name. It is stored here so that it is replaced or removed with the user:
	// a re-created user starts with no attachments.
	Attached []attachment `json:"attached,omitempty"`
}

// attachment is a managed policy attached to a user. PolicyID is the
// incarnation id of the policy at the time of the attachment. An attachment
// whose policy is missing, being deleted, or of another incarnation grants
// nothing.
type attachment struct {
	Name     string `json:"name"`
	PolicyID string `json:"policyId"`
}

// keyRecord is the stored form of an access key. Secret is the sealed secret
// (nonce followed by ciphertext).
type keyRecord struct {
	V       int       `json:"v"`
	User    string    `json:"user"`
	UserID  string    `json:"userId"`
	Created time.Time `json:"created"`
	Secret  []byte    `json:"secret"`
}

// policyRecord is the stored form of a user's inline policy document.
type policyRecord struct {
	V        int    `json:"v"`
	UserID   string `json:"userId"`
	Document string `json:"document"`
}

// managedRecord is the stored form of a managed policy. ID is the incarnation
// id of the policy: a policy deleted and created again under the same name
// gets a new id, so attachments of the old one never apply to the new one.
// Deleting marks a deletion in progress. Such a policy grants nothing, and it
// cannot be replaced until the record is removed or the deletion is undone.
type managedRecord struct {
	V        int    `json:"v"`
	ID       string `json:"id"`
	Document string `json:"document"`
	Deleting bool   `json:"deleting"`
}

// quotaRecord counts the managed policy slots in use. A slot is taken before
// a policy record is created and given back only once that record is known to
// be gone. The count is therefore never below the number of records. A crash
// between the two steps leaves a slot taken, which errs towards refusing.
type quotaRecord struct {
	V     int `json:"v"`
	Count int `json:"count"`
}

// recordCodec is strict: unknown fields are an error, so a record written by
// a newer version is never silently misread.
var recordCodec = sonic.Config{
	DisallowUnknownFields: true,
	CopyString:            true,
	ValidateString:        true,
}.Froze()

// encodeRecord marshals a record to compact JSON.
func encodeRecord(v any) ([]byte, error) {
	return recordCodec.Marshal(v)
}

// decodeUserRecord parses and checks a user record.
func decodeUserRecord(b []byte) (userRecord, error) {
	var r userRecord
	if err := recordCodec.Unmarshal(b, &r); err != nil {
		return userRecord{}, err
	}
	return r, checkVersion(r.V)
}

// decodeKeyRecord parses and checks an access key record.
func decodeKeyRecord(b []byte) (keyRecord, error) {
	var r keyRecord
	if err := recordCodec.Unmarshal(b, &r); err != nil {
		return keyRecord{}, err
	}
	return r, checkVersion(r.V)
}

// decodePolicyRecord parses and checks an inline policy record.
func decodePolicyRecord(b []byte) (policyRecord, error) {
	var r policyRecord
	if err := recordCodec.Unmarshal(b, &r); err != nil {
		return policyRecord{}, err
	}
	return r, checkVersion(r.V)
}

// decodeManagedRecord parses and checks a managed policy record.
func decodeManagedRecord(b []byte) (managedRecord, error) {
	var r managedRecord
	if err := recordCodec.Unmarshal(b, &r); err != nil {
		return managedRecord{}, err
	}
	return r, checkVersion(r.V)
}

// decodeQuotaRecord parses and checks the managed policy slot count.
func decodeQuotaRecord(b []byte) (quotaRecord, error) {
	var r quotaRecord
	if err := recordCodec.Unmarshal(b, &r); err != nil {
		return quotaRecord{}, err
	}
	return r, checkVersion(r.V)
}

// checkVersion refuses records of another version.
func checkVersion(v int) error {
	if v != recordVersion {
		return errRecordVersion
	}
	return nil
}

// ownedUser returns a predicate for deleteWhile that holds while a stored user
// record still belongs to the incarnation id. A record that does not decode
// is treated as owned, so that a corrupt leftover of a deleted user is cleared.
func ownedUser(id string) func([]byte) bool {
	return func(raw []byte) bool {
		rec, err := decodeUserRecord(raw)
		return err != nil || rec.ID == id
	}
}

// ownedPolicy returns a predicate for deleteWhile that holds while a stored
// inline policy record still belongs to the user incarnation id. As in
// ownedUser, a record that does not decode is treated as owned.
func ownedPolicy(id string) func([]byte) bool {
	return func(raw []byte) bool {
		rec, err := decodePolicyRecord(raw)
		return err != nil || rec.UserID == id
	}
}

// ownedManaged returns a predicate for deleteWhile that holds while a stored
// managed policy record still has the incarnation id. As in ownedUser, a
// record that does not decode is treated as owned.
func ownedManaged(id string) func([]byte) bool {
	return func(raw []byte) bool {
		rec, err := decodeManagedRecord(raw)
		return err != nil || rec.ID == id
	}
}
