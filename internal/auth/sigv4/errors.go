package sigv4

import (
	"errors"

	"github.com/eukalpia/unio/internal/s3err"
)

// ErrUnknownAccessKey is returned by a CredentialStore for an access key it
// does not know, or whose user is disabled. It maps to InvalidAccessKeyId.
var ErrUnknownAccessKey = s3err.NewSentinel("sigv4: unknown access key", s3err.InvalidAccessKeyID)

// errBadEscape reports a malformed percent-escape in a path or query.
var errBadEscape = errors.New("sigv4: malformed percent-escape")

// malformed returns an AuthorizationHeaderMalformed error with a client-safe
// message. Messages never include the signature, secrets or access keys.
func malformed(format string, args ...any) error {
	return s3err.Errorf(s3err.AuthorizationHeaderMalformed, format, args...)
}
