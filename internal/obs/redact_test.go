package obs

import (
	"net/http"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestIsSensitiveKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		key  string
		want bool
	}{
		{key: "secret_key", want: true},
		{key: "SecretAccessKey", want: true},
		{key: "password", want: true},
		{key: "Passwd", want: true},
		{key: "api_token", want: true},
		{key: "X-Amz-Signature", want: true},
		{key: "Authorization", want: true},
		{key: "Proxy-Authorization", want: true},
		{key: "Cookie", want: true},
		{key: "Set-Cookie", want: true},
		{key: "X-Amz-Credential", want: true},
		{key: "X-Amz-Security-Token", want: true},
		{key: "bucket", want: false},
		{key: "object_key", want: false},
		{key: "X-Amz-Request-Id", want: false},
		{key: "Content-Type", want: false},
		{key: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			if got := IsSensitiveKey(tc.key); got != tc.want {
				t.Fatalf("IsSensitiveKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestRedactHeaders(t *testing.T) {
	t.Parallel()
	in := http.Header{
		"Authorization":        {"AWS4-HMAC-SHA256 Credential=AKIA/20260101"},
		"Cookie":               {"session=abc", "lang=en"},
		"X-Amz-Security-Token": {"tok"},
		"Content-Type":         {"application/xml"},
		"X-Amz-Request-Id":     {"0123456789abcdef0123456789abcdef"},
	}
	want := http.Header{
		"Authorization":        {Redacted},
		"Cookie":               {Redacted},
		"X-Amz-Security-Token": {Redacted},
		"Content-Type":         {"application/xml"},
		"X-Amz-Request-Id":     {"0123456789abcdef0123456789abcdef"},
	}

	t.Run("redacts sensitive and keeps the rest", func(t *testing.T) {
		t.Parallel()
		got := RedactHeaders(in)
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("RedactHeaders mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("does not modify the input", func(t *testing.T) {
		t.Parallel()
		local := http.Header{"Authorization": {"Bearer s3cr3t"}}
		out := RedactHeaders(local)
		if got := local.Get("Authorization"); got != "Bearer s3cr3t" {
			t.Fatalf("input was modified: Authorization = %q", got)
		}
		out.Set("Content-Type", "text/plain")
		if got := local.Get("Content-Type"); got != "" {
			t.Fatalf("output shares storage with input: Content-Type = %q", got)
		}
	})

	t.Run("nil in nil out", func(t *testing.T) {
		t.Parallel()
		if got := RedactHeaders(nil); got != nil {
			t.Fatalf("RedactHeaders(nil) = %v, want nil", got)
		}
	})
}
