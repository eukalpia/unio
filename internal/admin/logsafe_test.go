package admin

import (
	"strings"
	"testing"
)

func TestLogSafe(t *testing.T) {
	cases := map[string]string{
		"ttpu-app-rw":                  "ttpu-app-rw",
		"evil\nlevel=ERROR msg=forged": "evillevel=ERROR msg=forged",
		"a\r\nb\tc\x00d\x1be":          "abcde",
		"политика":                     "политика",
	}
	for in, want := range cases {
		if got := logSafe(in); got != want {
			t.Errorf("logSafe(%q) = %q, want %q", in, got, want)
		}
	}
	if got := logSafe(strings.Repeat("x", 1000)); len(got) != maxLoggedValue {
		t.Errorf("length = %d, want %d", len(got), maxLoggedValue)
	}
}

func TestSecretsEqual(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"secret-key-0123456789", "secret-key-0123456789", true},
		{"secret-key-0123456789", "secret-key-012345678", false},
		{"secret-key-0123456789", "secret-key-0123456780", false},
		{"", "", true},
		{"abc", "", false},
		{strings.Repeat("s", maxSecretLen+1), strings.Repeat("s", maxSecretLen+1)[:maxSecretLen], false},
	}
	for _, c := range cases {
		if got := secretsEqual(c.a, c.b); got != c.want {
			t.Errorf("secretsEqual(%d bytes, %d bytes) = %v, want %v", len(c.a), len(c.b), got, c.want)
		}
	}
}
