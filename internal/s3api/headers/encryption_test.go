package headers

import (
	"net/http"
	"testing"

	"github.com/eukalpia/unio/internal/s3err"
)

// TestRejectEncryption checks that every request header that asks for
// server-side encryption is refused with NotImplemented, and that headers with
// similar names are not.
func TestRejectEncryption(t *testing.T) {
	refused := []string{
		"x-amz-server-side-encryption",
		"x-amz-server-side-encryption-aws-kms-key-id",
		"x-amz-server-side-encryption-context",
		"x-amz-server-side-encryption-bucket-key-enabled",
		"x-amz-server-side-encryption-customer-algorithm",
		"x-amz-server-side-encryption-customer-key",
		"x-amz-server-side-encryption-customer-key-md5",
		"x-amz-copy-source-server-side-encryption-customer-algorithm",
		"x-amz-copy-source-server-side-encryption-customer-key",
		"x-amz-copy-source-server-side-encryption-customer-key-md5",
	}
	for _, name := range refused {
		h := http.Header{}
		h.Set(name, "value")
		if err := RejectEncryption(h); !s3err.Is(err, s3err.NotImplemented) {
			t.Fatalf("%s: err = %v, want NotImplemented", name, err)
		}
	}

	allowed := []http.Header{
		{},
		{"X-Amz-Meta-Server-Side-Encryption": []string{"note"}},
		{"X-Amz-Storage-Class": []string{"STANDARD"}},
		{"X-Amz-Checksum-Sha256": []string{"abc"}},
	}
	for _, h := range allowed {
		if err := RejectEncryption(h); err != nil {
			t.Fatalf("%v: err = %v, want nil", h, err)
		}
	}
}
