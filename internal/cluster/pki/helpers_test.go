package pki

import (
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// mustCA derives a CA from secret or fails the test.
func mustCA(t *testing.T, secret []byte) *CA {
	t.Helper()
	ca, err := DeriveCA(secret)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

// mustIdentity issues a node identity from ca or fails the test.
func mustIdentity(t *testing.T, ca *CA, id ids.NodeID) *Identity {
	t.Helper()
	ident, err := ca.Identity(id, []string{"127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	return ident
}
