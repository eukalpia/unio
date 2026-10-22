package auth

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

const (
	testRootKey    = "ROOTKEY00000001234"
	testRootSecret = "rootsecret-0123456789-abcdefghijklmnopqrstuvwxyz"
	// strongSecret is a 33-character alphanumeric secret with many distinct characters.
	strongSecret = "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0jA"
)

func TestValidateRootCredentials(t *testing.T) {
	cases := []struct {
		name    string
		ak, sk  string
		wantErr string // substring of the error; empty means accepted
	}{
		{name: "strong pair", ak: testRootKey, sk: testRootSecret},
		{name: "strong alphanumeric secret", ak: testRootKey, sk: strongSecret},

		{name: "access key of 11 characters", ak: "ABCDEFGHIJK", sk: testRootSecret, wantErr: "at least 12"},
		{name: "access key of 12 characters", ak: "ABCDEFGHIJKL", sk: testRootSecret},
		{name: "access key of 128 characters", ak: strings.Repeat("A", 128), sk: testRootSecret},
		{name: "access key of 129 characters", ak: strings.Repeat("A", 129), sk: testRootSecret, wantErr: "at most 128"},
		{name: "access key with a hyphen", ak: "unio-root-adm01", sk: testRootSecret, wantErr: "only letters and digits"},
		{name: "access key with a slash", ak: "ABCDEFGHIJK/L", sk: testRootSecret, wantErr: "only letters and digits"},
		{name: "well-known user name", ak: "administrator", sk: testRootSecret, wantErr: "well-known default name"},
		{name: "well-known user name in mixed case", ak: "Administrator", sk: testRootSecret, wantErr: "well-known default name"},
		{name: "well-known user name in upper case", ak: "ADMINISTRATOR", sk: testRootSecret, wantErr: "well-known default name"},

		{name: "secret of 31 characters", ak: testRootKey, sk: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 3)[:31], wantErr: "at least 32"},
		{name: "secret of 32 characters", ak: testRootKey, sk: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 3)[:32]},
		{name: "secret of 256 characters", ak: testRootKey, sk: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 18) + "Ab3C"},
		{name: "secret of 257 characters", ak: testRootKey, sk: strings.Repeat("Ab3Cd5Ef7Gh9Jk", 18) + "Ab3Cd", wantErr: "at most 256"},
		{name: "secret with a space", ak: testRootKey, sk: "rootsecret-0123456789-abcdefghijk lmnop", wantErr: "printable characters without spaces"},
		{name: "secret with a non-ASCII character", ak: testRootKey, sk: "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0é", wantErr: "printable characters without spaces"},
		{name: "secret with nine distinct characters", ak: testRootKey, sk: strings.Repeat("abcdefghi", 4), wantErr: "at least 10 distinct characters"},
		{name: "secret with ten distinct characters", ak: testRootKey, sk: strings.Repeat("abcdefghij", 4)},
		{name: "secret repeated character", ak: testRootKey, sk: strings.Repeat("a", 40), wantErr: "at least 10 distinct characters"},
		{name: "secret equal to access key", ak: strongSecret, sk: strongSecret, wantErr: "must differ from the root access key"},
		{name: "secret with changeme", ak: testRootKey, sk: "ChangeMe-Please-Rotate-This-Value-9876", wantErr: "template or documentation example"},
		{name: "secret with minioadmin", ak: testRootKey, sk: "MinIOAdmin-Rotate-This-Value-Now-0001", wantErr: "template or documentation example"},
		{name: "secret with replace_with", ak: testRootKey, sk: "REPLACE_WITH_UNIO_INIT_OUTPUT_LONG_ENOUGH", wantErr: "template or documentation example"},
		{name: "secret with placeholder", ak: testRootKey, sk: "placeholder-value-for-the-root-secret-key-x", wantErr: "template or documentation example"},
		{name: "secret with example", ak: testRootKey, sk: "wJalrXUtnFEMI-EXAMPLE-key-material-0000", wantErr: "template or documentation example"},
		{name: "secret with password", ak: testRootKey, sk: "my-password-is-long-enough-123456", wantErr: "word password"},
		{name: "secret with password in upper case", ak: testRootKey, sk: "MY-PASSWORD-IS-LONG-ENOUGH-123456", wantErr: "word password"},
		{name: "empty", ak: "", sk: "", wantErr: "at least 12"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRootCredentials(c.ak, c.sk)
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRootCredentials = %v, want accepted", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("ValidateRootCredentials = %v, want error containing %q", err, c.wantErr)
			}
			if !s3err.Is(err, s3err.InvalidArgument) {
				t.Fatalf("code = %s, want InvalidArgument", s3err.CodeOf(err))
			}
			if strings.Contains(err.Error(), c.sk) && c.sk != "" {
				t.Fatalf("error %q echoes the secret", err)
			}
		})
	}
}

func TestValidAccessKeyID(t *testing.T) {
	valid := []string{"ABCDEFGHIJKL", "abcdefghijkl0123", strings.Repeat("A", 128)}
	invalid := []string{"", "ABCDEFGHIJK", "ABCDEFGHIJK-L", "ABCDEFGHIJK_L", "ABCDEFGHIJK/", "ABCDEFGHIJK L", strings.Repeat("A", 129)}
	for _, id := range valid {
		if !ValidAccessKeyID(id) {
			t.Errorf("ValidAccessKeyID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if ValidAccessKeyID(id) {
			t.Errorf("ValidAccessKeyID(%q) = true, want false", id)
		}
	}
}
