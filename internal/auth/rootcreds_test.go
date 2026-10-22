package auth

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

const (
	testRootKey    = "ROOTKEY00000001234"
	testRootSecret = "rootsecret-0123456789-abcdefghijklmnopqrstuvwxyz"
)

func TestValidateRootCredentials(t *testing.T) {
	cases := []struct {
		name   string
		ak, sk string
		ok     bool
	}{
		{"good", testRootKey, testRootSecret, true},
		{"access key too short", "SHORTKEY1234", testRootSecret, true},
		{"access key 11 chars", "ABCDEFGHIJK", testRootSecret, false},
		{"access key with slash", "ABCDEFGHIJK/L", testRootSecret, false},
		{"access key with hyphen", "unio-abcdef0123", testRootSecret, false},
		{"secret too short", testRootKey, "abcdefghijklmnopqrstuvwxyz01234", false},
		{"secret with space", testRootKey, "rootsecret-0123456789-abcdefghijk lmnop", false},
		{"secret repeated char", testRootKey, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"secret too long", testRootKey, strings.Repeat("a", 300), false},
		{"empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateRootCredentials(c.ak, c.sk)
			if (err == nil) != c.ok {
				t.Fatalf("ValidateRootCredentials = %v, want ok=%v", err, c.ok)
			}
			if err != nil && !s3err.Is(err, s3err.InvalidArgument) {
				t.Fatalf("code = %s, want InvalidArgument", s3err.CodeOf(err))
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
