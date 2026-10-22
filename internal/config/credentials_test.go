package config

import (
	"strings"
	"testing"
)

func TestCheckRootUser(t *testing.T) {
	tests := []struct {
		name    string
		user    string
		wantErr string
	}{
		{name: "strong", user: "unioRootAdm01"},
		{name: "exactly minimum", user: strings.Repeat("a", 12)},
		{name: "too short", user: strings.Repeat("a", 11), wantErr: "at least 12"},
		{name: "contains space", user: "uni root admin", wantErr: "printable ASCII"},
		{name: "contains non ascii", user: "unioRootAdmé", wantErr: "printable ASCII"},
		{name: "default name", user: "administrator", wantErr: "well-known default"},
		{name: "default name in other case", user: "MinIOAdmin", wantErr: "at least 12"},
		{name: "default name mixed case", user: "MINIOADMIN", wantErr: "at least 12"},
		{name: "empty", user: "", wantErr: "at least 12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRootUser(tt.user)
			checkCredentialErr(t, err, tt.wantErr)
		})
	}
}

func TestCheckRootPassword(t *testing.T) {
	strong := "Qv8mT2zK9pL4xR7nB1cW6yH3dF5gS0jA"
	tests := []struct {
		name     string
		user     string
		password string
		wantErr  string
	}{
		{name: "strong", user: "unioRootAdm01", password: strong},
		{name: "too short", user: "unioRootAdm01", password: strings.Repeat("Qv8mT2zK9p", 3), wantErr: "at least 32"},
		{name: "space", user: "unioRootAdm01", password: strong[:16] + " " + strong[17:], wantErr: "printable ASCII"},
		{name: "low entropy", user: "unioRootAdm01", password: strings.Repeat("ab", 16), wantErr: "distinct characters"},
		{name: "equal to user", user: strong, password: strong, wantErr: "must differ"},
		{name: "contains password", user: "unioRootAdm01", password: "my-password-is-long-enough-123456", wantErr: "well-known default"},
		{name: "example key", user: "unioRootAdm01", password: "wJalrXUtnFEMI-EXAMPLE-key-material-0000", wantErr: "well-known default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckRootPassword(tt.user, tt.password)
			checkCredentialErr(t, err, tt.wantErr)
		})
	}
}

func TestCheckSecretValue(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "strong", value: testSecret},
		{name: "too short", value: strings.Repeat("ab", 15), wantErr: "at least 32"},
		{name: "low entropy", value: strings.Repeat("a", 40), wantErr: "distinct characters"},
		{name: "template placeholder", value: "REPLACE_WITH_UNIO_INIT_OUTPUT_LONG_ENOUGH", wantErr: "template"},
		{name: "changeme", value: "changeme-changeme-changeme-changeme-x", wantErr: "template"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkCredentialErr(t, CheckSecretValue(tt.value), tt.wantErr)
		})
	}
}

// checkCredentialErr asserts the error matches wantErr (a substring, or "" for success),
// and that the error text never echoes the checked value.
func checkCredentialErr(t *testing.T, err error, wantErr string) {
	t.Helper()
	if wantErr == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error = %v, want substring %q", err, wantErr)
	}
}
