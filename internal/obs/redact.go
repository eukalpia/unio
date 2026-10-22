package obs

import (
	"log/slog"
	"net/http"
	"strings"
)

// Redacted is the placeholder that replaces the value of a secret.
const Redacted = "[REDACTED]"

// IsSensitiveKey reports whether a log key or header name names a secret. It
// matches, case-insensitively and by substring, secret, password, passwd,
// token, signature, authorization, cookie, credential or x-amz-security-token.
func IsSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, frag := range [...]string{
		"secret",
		"password",
		"passwd",
		"token",
		"signature",
		"authorization",
		"cookie",
		"credential",
		"x-amz-security-token",
	} {
		if strings.Contains(k, frag) {
			return true
		}
	}
	return false
}

// RedactHeaders returns a copy of h with the values of sensitive headers
// replaced by Redacted. The input is not modified. A nil header yields nil.
func RedactHeaders(h http.Header) http.Header {
	if h == nil {
		return nil
	}
	out := make(http.Header, len(h))
	for k, vs := range h {
		if IsSensitiveKey(k) {
			out[k] = []string{Redacted}
			continue
		}
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// replaceAttr is the slog ReplaceAttr hook. It redacts the value of an
// attribute whose key is sensitive, or that sits inside a sensitive group.
// It also redacts sensitive header maps logged as values.
//
// slog does not pass group-kind attributes to this hook, so a sensitive group
// name is enforced on each leaf through the groups argument. The structure of
// the output is kept, and only the values are replaced.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if IsSensitiveKey(a.Key) || underSensitiveGroup(groups) {
		return slog.String(a.Key, Redacted)
	}
	if h, ok := a.Value.Any().(http.Header); ok {
		return slog.Any(a.Key, RedactHeaders(h))
	}
	return a
}

// underSensitiveGroup reports whether any enclosing group name is sensitive.
func underSensitiveGroup(groups []string) bool {
	for _, g := range groups {
		if IsSensitiveKey(g) {
			return true
		}
	}
	return false
}
