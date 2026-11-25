package config

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/auth/authtest"
	"github.com/eukalpia/unio/internal/auth/iam"
	"github.com/eukalpia/unio/internal/s3err"
)

// rootCase is one root credential pair with the verdict that both configuration
// and the node must reach.
type rootCase struct {
	name   string
	user   string
	secret string
	ok     bool
}

// rootCases is the shared table. Configuration validation and iam.New must give
// the same verdict on every row, so the node never starts with credentials that
// configuration accepted, or the other way round.
var rootCases = []rootCase{
	{name: "strong pair", user: testRootUser, secret: testRootPassword, ok: true},
	{name: "user of 11 characters", user: "ABCDEFGHIJK", secret: testRootPassword},
	{name: "user of 12 characters", user: "ABCDEFGHIJKL", secret: testRootPassword, ok: true},
	{name: "user of 128 characters", user: strings.Repeat("R", 128), secret: testRootPassword, ok: true},
	{name: "user of 129 characters", user: strings.Repeat("R", 129), secret: testRootPassword},
	{name: "user with a hyphen", user: "unio-root-adm01", secret: testRootPassword},
	{name: "empty user", user: "", secret: testRootPassword},
	{name: "well-known user", user: "administrator", secret: testRootPassword},
	{name: "well-known user in mixed case", user: "Administrator", secret: testRootPassword},
	{name: "secret of 31 characters", user: testRootUser, secret: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 3)[:31]},
	{name: "secret of 32 characters", user: testRootUser, secret: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 3)[:32], ok: true},
	{name: "secret of 256 characters", user: testRootUser, secret: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 18) + "Ab3C", ok: true},
	{name: "secret of 257 characters", user: testRootUser, secret: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 18) + "Ab3Cd"},
	{name: "secret with a space", user: testRootUser, secret: "Pw7Kd2Rx9 Lm4Vq8Zt1Nb6Hc3Yf5Gs0Ja"},
	{name: "secret with a non-ASCII character", user: testRootUser, secret: "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0é"},
	{name: "secret with nine distinct characters", user: testRootUser, secret: strings.Repeat("abcdefghi", 4)},
	{name: "secret with ten distinct characters", user: testRootUser, secret: strings.Repeat("abcdefghij", 4), ok: true},
	{name: "secret equal to the user", user: "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0jA", secret: "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0jA"},
	{name: "secret equal to the user in other case", user: "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0jA", secret: "qv8mt2zk9pl4xr7nb1cw6yh3df5gs0ja", ok: true},
	{name: "secret with changeme", user: testRootUser, secret: "ChangeMe-Please-Rotate-This-Value-9876"},
	{name: "secret with minioadmin", user: testRootUser, secret: "MinIOAdmin-Rotate-This-Value-Now-0001"},
	{name: "secret with replace_with", user: testRootUser, secret: "REPLACE_WITH_UNIO_INIT_OUTPUT_LONG_ENOUGH"},
	{name: "secret with placeholder", user: testRootUser, secret: "placeholder-value-for-the-root-secret-key-x"},
	{name: "secret with example", user: testRootUser, secret: "wJalrXUtnFEMI-EXAMPLE-key-material-0000"},
	{name: "secret with password", user: testRootUser, secret: "my-password-is-long-enough-123456"},
	{name: "secret with password in upper case", user: testRootUser, secret: "MY-PASSWORD-IS-LONG-ENOUGH-123456"},
	{name: "random pair", user: "KQ3M7XZ2P5VWY4HJBNDR", secret: "Yc7Lm2Qp9Wr4Tz8Vb1Nx6Hs3Kd5Fg0Ja", ok: true},
}

// TestRootCredentialVerdictsMatchNode checks that configuration and iam.New
// accept and refuse the same root credentials, and that both match the table.
func TestRootCredentialVerdictsMatchNode(t *testing.T) {
	for _, tc := range rootCases {
		t.Run(tc.name, func(t *testing.T) {
			cfgOK := configAcceptsRoot(t, tc.user, tc.secret)
			iamOK := iamAcceptsRoot(t, tc.user, tc.secret)
			if cfgOK != iamOK {
				t.Fatalf("configuration accepts = %v but iam.New accepts = %v", cfgOK, iamOK)
			}
			if cfgOK != tc.ok {
				t.Fatalf("accepted = %v, want %v", cfgOK, tc.ok)
			}
		})
	}
}

// configAcceptsRoot reports whether configuration validation accepts the pair.
// Every other setting of validConfig is valid, so only the root check can fail.
func configAcceptsRoot(t *testing.T, user, secret string) bool {
	t.Helper()
	c := validConfig()
	c.Auth.RootUser = NewSecret(user)
	c.Auth.RootPassword = NewSecret(secret)
	_, err := Validate(&c)
	if err == nil {
		return true
	}
	if !strings.Contains(err.Error(), "root credentials are refused") && !strings.Contains(err.Error(), "auth.root_user is not set") {
		t.Fatalf("unexpected validation error: %v", err)
	}
	return false
}

// iamAcceptsRoot reports whether iam.New accepts the pair. A refusal must be the
// root credential rule, an InvalidArgument error, and not a storage or key error.
func iamAcceptsRoot(t *testing.T, user, secret string) bool {
	t.Helper()
	_, err := iam.New(authtest.NewMemKV(), iam.Config{
		KEK:           iam.DeriveKEK([]byte(testSecret)),
		RootAccessKey: user,
		RootSecret:    secret,
	})
	if err == nil {
		return true
	}
	if !s3err.Is(err, s3err.InvalidArgument) {
		t.Fatalf("iam.New refused with a non-credential error: %v", err)
	}
	return false
}
