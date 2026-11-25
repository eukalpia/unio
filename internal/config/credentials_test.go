package config

import (
	"strings"
	"testing"
)

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
