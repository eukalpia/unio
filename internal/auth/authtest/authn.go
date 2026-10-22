package authtest

import (
	"context"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/auth/sigv4"
)

// TestRegion is the region of the fake authenticator.
const TestRegion = "us-east-1"

// staticStore is a sigv4.CredentialStore that knows one key pair.
type staticStore struct {
	accessKey string
	secret    string
}

// Lookup implements sigv4.CredentialStore.
func (s staticStore) Lookup(_ context.Context, ak string) (string, auth.Identity, error) {
	if ak != s.accessKey {
		return "", auth.Identity{}, sigv4.ErrUnknownAccessKey
	}
	return s.secret, auth.Identity{AccessKey: ak, User: "test", Root: true}, nil
}

// NewAuthenticator returns an Authenticator that accepts requests signed
// with accessKey and secret in TestRegion. Signatures are verified by the
// real SigV4 implementation. The identity is a root identity, so policies
// do not restrict it. The clock is the system clock unless clock is non-nil.
func NewAuthenticator(accessKey, secret string, clock func() time.Time) auth.Authenticator {
	return sigv4.New(staticStore{accessKey: accessKey, secret: secret}, sigv4.Options{
		Region: TestRegion,
		Clock:  clock,
	})
}
