package integration

import (
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/aws/smithy-go"
)

func TestRandomBucketName(t *testing.T) {
	t.Parallel()
	shape := regexp.MustCompile(`^unio-it-[0-9a-f]{12}$`)
	seen := make(map[string]bool)
	for range 64 {
		name := randomBucketName(t)
		if !shape.MatchString(name) {
			t.Fatalf("randomBucketName() = %q, want unio-it- plus 12 lowercase hex characters", name)
		}
		if seen[name] {
			t.Fatalf("randomBucketName() repeated %q", name)
		}
		seen[name] = true
	}
}

func TestCopySourceValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		bucket string
		key    string
		want   string
	}{
		{name: "plain key", bucket: "b", key: "k", want: "b/k"},
		{name: "slashes kept", bucket: "b", key: "dir/sub/k", want: "b/dir/sub/k"},
		{name: "space and plus escaped", bucket: "b", key: "dir/a b+c.txt", want: "b/dir/a%20b%2Bc.txt"},
		{name: "unicode escaped", bucket: "b", key: "ünï/x", want: "b/%C3%BCn%C3%AF/x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := copySourceValue(tc.bucket, tc.key); got != tc.want {
				t.Fatalf("copySourceValue(%q, %q) = %q, want %q", tc.bucket, tc.key, got, tc.want)
			}
		})
	}
}

func TestErrorCode(t *testing.T) {
	t.Parallel()
	api := &smithy.GenericAPIError{Code: "NoSuchKey", Message: "missing"}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil error", err: nil, want: ""},
		{name: "plain error", err: errors.New("boom"), want: ""},
		{name: "api error", err: api, want: "NoSuchKey"},
		{name: "wrapped api error", err: fmt.Errorf("get: %w", api), want: "NoSuchKey"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errorCode(tc.err); got != tc.want {
				t.Fatalf("errorCode() = %q, want %q", got, tc.want)
			}
		})
	}
}
