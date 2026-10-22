package iam

import (
	"time"

	"github.com/bytedance/sonic"
)

// recordVersion is the version of every record written by this package.
// Records with another version are refused.
const recordVersion = 1

// Key prefixes under the SystemKV namespace.
const (
	usersPrefix    = "iam/users/"
	keysPrefix     = "iam/keys/"
	policiesPrefix = "iam/policies/"
)

// userPath returns the SystemKV key of a user record.
func userPath(name string) string { return usersPrefix + name }

// keyPath returns the SystemKV key of an access key record.
func keyPath(accessKey string) string { return keysPrefix + accessKey }

// policyPath returns the SystemKV key of a user's policy record.
func policyPath(name string) string { return policiesPrefix + name }

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

// policyRecord is the stored form of a user's policy document.
type policyRecord struct {
	V        int    `json:"v"`
	UserID   string `json:"userId"`
	Document string `json:"document"`
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

// decodePolicyRecord parses and checks a policy record.
func decodePolicyRecord(b []byte) (policyRecord, error) {
	var r policyRecord
	if err := recordCodec.Unmarshal(b, &r); err != nil {
		return policyRecord{}, err
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
