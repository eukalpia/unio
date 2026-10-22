package sigv4

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

const (
	// algorithm is the SigV4 algorithm identifier.
	algorithm = "AWS4-HMAC-SHA256"
	// service is the only service this package signs for.
	service = "s3"
	// terminator closes every credential scope.
	terminator = "aws4_request"
	// amzLayout is the ISO 8601 basic format used by X-Amz-Date.
	amzLayout = "20060102T150405Z"
	// dateLayout is the date part of the credential scope.
	dateLayout = "20060102"
	// emptyPayloadSHA256 is the hex SHA-256 of an empty payload.
	emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	// unsignedPayload is the payload hash for unsigned bodies.
	unsignedPayload = "UNSIGNED-PAYLOAD"
)

// hmacSHA256 returns HMAC-SHA256(key, data).
func hmacSHA256(key, data []byte) [sha256.Size]byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	var out [sha256.Size]byte
	m.Sum(out[:0])
	return out
}

// stringToSign builds the SigV4 string to sign from the request time, the
// credential scope and the hex SHA-256 of the canonical request.
func stringToSign(amzDate, scope, canonicalHash string) string {
	return algorithm + "\n" + amzDate + "\n" + scope + "\n" + canonicalHash
}

// sha256Hex returns the lower-case hex SHA-256 of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
