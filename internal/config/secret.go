package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"go.yaml.in/yaml/v3"
)

// errSecretInFile is returned when a secret appears in the YAML file.
// Secrets come from the environment only, so they never sit in a file that
// may be committed or copied.
var errSecretInFile = errors.New("secret values must not appear in the config file; " +
	"set the matching UNIO_* environment variable or its _FILE variant instead")

// Secret holds a sensitive value such as a key, a password or a token.
// Every representation except Expose redacts the value, so it cannot leak
// through logs, fmt verbs, JSON or YAML output.
type Secret struct {
	value string
}

// NewSecret wraps v as a Secret.
func NewSecret(v string) Secret { return Secret{value: v} }

// Expose returns the raw value. Call it only where the value is used,
// never to build output.
func (s Secret) Expose() string { return s.value }

// IsSet reports whether the secret holds a non-empty value.
func (s Secret) IsSet() bool { return s.value != "" }

// redacted returns the placeholder shown for a set secret, or "" when unset,
// so that an unset value is still visible in printed configuration.
func (s Secret) redacted() string {
	if s.value == "" {
		return ""
	}
	return Redacted
}

// Redacted is the placeholder printed in place of a secret value.
const Redacted = "[REDACTED]"

// String implements fmt.Stringer and redacts the value.
func (s Secret) String() string { return s.redacted() }

// GoString implements fmt.GoStringer and redacts the value.
func (s Secret) GoString() string { return fmt.Sprintf("config.Secret(%q)", s.redacted()) }

// Format implements fmt.Formatter so that every verb, including %#v and
// %+v, prints the redacted form.
func (s Secret) Format(f fmt.State, _ rune) {
	_, _ = fmt.Fprint(f, s.redacted())
}

// LogValue implements slog.LogValuer so a Secret logged as an attribute is
// redacted.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.redacted()) }

// MarshalJSON writes the redacted form.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.redacted()) }

// MarshalYAML writes the redacted form.
func (s Secret) MarshalYAML() (any, error) { return s.redacted(), nil }

// UnmarshalYAML rejects any secret written in the configuration file.
func (s *Secret) UnmarshalYAML(*yaml.Node) error { return errSecretInFile }
