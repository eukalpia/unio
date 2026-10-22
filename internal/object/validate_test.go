package object

import (
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

func TestValidateBucketName(t *testing.T) {
	good := []string{"abc", "my-bucket", "my.bucket.1", strings.Repeat("a", 63)}
	bad := []string{"ab", strings.Repeat("a", 64), "Abc", "-abc", "abc-", "a..b", "a.-b", "192.168.1.1", "xn--abc", "abc-s3alias", "a_b"}
	for _, n := range good {
		if err := ValidateBucketName(n); err != nil {
			t.Fatalf("%q: unexpected %v", n, err)
		}
	}
	for _, n := range bad {
		if err := ValidateBucketName(n); !s3err.Is(err, s3err.InvalidBucketName) {
			t.Fatalf("%q: want InvalidBucketName, got %v", n, err)
		}
	}
}

func TestValidateKey(t *testing.T) {
	if err := ValidateKey("a/b/c.txt"); err != nil {
		t.Fatal(err)
	}
	if !s3err.Is(ValidateKey(strings.Repeat("k", MaxKeyLength+1)), s3err.KeyTooLongError) {
		t.Fatal("want KeyTooLongError")
	}
	for _, k := range []string{"", "a\x00b", string([]byte{0xff, 0xfe})} {
		if !s3err.Is(ValidateKey(k), s3err.InvalidArgument) {
			t.Fatalf("%q: want InvalidArgument", k)
		}
	}
}

func TestValidateUserMetadata(t *testing.T) {
	if err := ValidateUserMetadata(map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	big := map[string]string{"k": strings.Repeat("v", MaxUserMetadataSize)}
	if !s3err.Is(ValidateUserMetadata(big), s3err.MetadataTooLarge) {
		t.Fatal("want MetadataTooLarge")
	}
}

func FuzzValidateKey(f *testing.F) {
	for _, s := range []string{"a", "a/b", "", "\x00", strings.Repeat("x", 1025)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, key string) {
		if err := ValidateKey(key); err == nil {
			if key == "" || len(key) > MaxKeyLength || strings.IndexByte(key, 0) >= 0 {
				t.Fatalf("accepted invalid key %q", key)
			}
		}
	})
}
