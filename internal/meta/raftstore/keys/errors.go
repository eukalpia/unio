package keys

import "errors"

// ErrMalformed is wrapped by every error that a Parse function returns for
// input that is not a valid key of the expected kind. Test it with errors.Is.
var ErrMalformed = errors.New("keys: malformed key")
