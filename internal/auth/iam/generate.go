package iam

import (
	"crypto/rand"
	"encoding/hex"
	"io"
)

// Lengths and alphabets of generated credentials.
const (
	// accessKeyIDLen is the length of a generated access key ID.
	accessKeyIDLen = 20
	// accessKeyIDAlphabet is the upper-case letters and digits.
	accessKeyIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// secretLen is the length of a generated secret key.
	secretLen = 40
	// secretAlphabet is the 64 symbols of base64, so every byte maps evenly.
	secretAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	// userIDBytes is the size of a random user id.
	userIDBytes = 16
)

// randomChars returns n characters drawn uniformly from alphabet, using
// crypto/rand. Bytes that would bias the distribution are rejected.
func randomChars(alphabet string, n int) (string, error) {
	// Accept only bytes below the largest multiple of len(alphabet).
	limit := 256 - 256%len(alphabet)
	out := make([]byte, 0, n)
	buf := make([]byte, n)
	for len(out) < n {
		if _, err := io.ReadFull(rand.Reader, buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, alphabet[int(b)%len(alphabet)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// newAccessKeyID returns a random 20-character upper-case access key ID.
func newAccessKeyID() (string, error) {
	return randomChars(accessKeyIDAlphabet, accessKeyIDLen)
}

// newSecret returns a random 40-character secret key.
func newSecret() (string, error) {
	return randomChars(secretAlphabet, secretLen)
}

// newUserID returns a random identifier for a user incarnation.
func newUserID() (string, error) {
	b := make([]byte, userIDBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
