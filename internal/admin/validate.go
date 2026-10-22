package admin

import (
	"net/url"
	"strconv"
)

// maxUserNameLen bounds an IAM user name. validUserName mirrors the rule of
// internal/auth/iam, which keeps its own copy unexported. A test pins the two.
const maxUserNameLen = 64

// maxParamLen bounds a free-text query parameter that is not a key, such as
// a delimiter.
const maxParamLen = 1024

// maxKeyQuery bounds the raw key of a delete. It is larger than the longest
// valid key, so that a longer key reaches object.ValidateKey and is refused
// with KeyTooLongError, as on the S3 API.
const maxKeyQuery = 4096

// maxHostLen bounds the Host header value that a presigned URL is built on.
const maxHostLen = 255

// Limits of the object listing.
const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

// validUserName reports whether name is an acceptable IAM user name: 1 to 64
// characters, starting with a letter or digit, and continuing with letters,
// digits or ._@-.
func validUserName(name string) bool {
	if len(name) == 0 || len(name) > maxUserNameLen {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if isAlnum(c) {
			continue
		}
		if i > 0 && (c == '.' || c == '_' || c == '@' || c == '-') {
			continue
		}
		return false
	}
	return true
}

// isAlnum reports whether c is an ASCII letter or digit.
func isAlnum(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

// checkUserName validates a user name from a path or a body. It refuses the
// root account's name, because the root user is not an IAM user and cannot be
// changed through this API.
func (s *Service) checkUserName(name string) error {
	if !validUserName(name) {
		return errInvalid("The user name is not valid.")
	}
	if name == s.cfg.RootAccessKey {
		return errRootProtected()
	}
	return nil
}

// validHost reports whether host has the form of a Host header value: a name or
// an address, bracketed for IPv6, with an optional numeric port. A presigned URL
// is built on it, so no other byte is allowed, and the brackets must balance.
func validHost(host string) bool {
	if host == "" || len(host) > maxHostLen {
		return false
	}
	for i := 0; i < len(host); i++ {
		c := host[i]
		switch {
		case isAlnum(c), c == '.', c == '-', c == ':', c == '[', c == ']':
		default:
			return false
		}
	}
	u, err := url.Parse("http://" + host)
	return err == nil && u.Host == host
}

// queryValue returns the single value of a query parameter. ok is false when
// the parameter is absent, and an error when it is given more than once.
func queryValue(q url.Values, name string) (string, bool, error) {
	vals, ok := q[name]
	switch {
	case !ok:
		return "", false, nil
	case len(vals) != 1:
		return "", false, errBadRequest("A query parameter was given more than once.")
	}
	return vals[0], true, nil
}

// boundedQuery returns a text query parameter, which may be empty, and refuses a
// value longer than max bytes.
func boundedQuery(q url.Values, name string, max int) (string, error) {
	v, _, err := queryValue(q, name)
	if err != nil {
		return "", err
	}
	if len(v) > max {
		return "", errInvalid("A query parameter is too long.")
	}
	return v, nil
}

// requiredQuery returns a query parameter that must be present, and bounds it
// like boundedQuery.
func requiredQuery(q url.Values, name string, max int) (string, error) {
	v, ok, err := queryValue(q, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errInvalid("A required query parameter is missing.")
	}
	if len(v) > max {
		return "", errInvalid("A query parameter is too long.")
	}
	return v, nil
}

// listLimitParam parses the max query parameter of an object listing. It is a
// decimal number from 1 to 1000, and defaults to 200. Signs and spaces are
// refused, so the value is always a plain count.
func listLimitParam(q url.Values) (int, error) {
	v, ok, err := queryValue(q, "max")
	if err != nil {
		return 0, err
	}
	if !ok {
		return defaultListLimit, nil
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, errInvalid("max must be a number from 1 to 1000.")
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxListLimit {
		return 0, errInvalid("max must be a number from 1 to 1000.")
	}
	return n, nil
}
