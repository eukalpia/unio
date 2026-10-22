package headers

import (
	"net/http"
	"strings"

	"github.com/eukalpia/unio/internal/s3err"
)

// encryptionPrefixes are the prefixes of every request header that asks for
// server-side encryption: the SSE-S3, SSE-KMS and SSE-C headers, and the
// copy-source form of the SSE-C headers. User metadata is sent under
// x-amz-meta-, so a name that merely contains the words is not matched.
var encryptionPrefixes = [...]string{
	"x-amz-server-side-encryption",
	"x-amz-copy-source-server-side-encryption",
}

// RejectEncryption returns NotImplemented when a request carries a header that
// asks for server-side encryption. This server stores objects without
// encryption, so accepting such a request would store the data in clear while
// the client believes it is protected. Every form is refused: SSE-S3, SSE-KMS
// with its key id, encryption context and bucket-key option, SSE-C customer
// keys, and their copy-source variants.
func RejectEncryption(h http.Header) error {
	for name := range h {
		lower := strings.ToLower(name)
		for _, p := range encryptionPrefixes {
			if strings.HasPrefix(lower, p) {
				return s3err.Errorf(s3err.NotImplemented, "server-side encryption is not supported by this server; the request must not ask for it")
			}
		}
	}
	return nil
}
