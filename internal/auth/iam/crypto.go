package iam

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// kekInfo is the HKDF info string that binds the key-encryption key to this
// purpose and version. Changing it makes stored secrets unreadable.
const kekInfo = "unio iam kek v1"

// kekLen is the key-encryption key length: AES-256.
const kekLen = 32

// errSealedTooShort reports a sealed value shorter than its nonce.
var errSealedTooShort = errors.New("iam: sealed value is too short")

// DeriveKEK derives the 32-byte key-encryption key for IAM secrets from the
// cluster secret, with HKDF-SHA256 and the info string "unio iam kek v1". It
// returns nil when the cluster secret is empty or derivation fails. New
// refuses a nil key.
func DeriveKEK(clusterSecret []byte) []byte {
	if len(clusterSecret) == 0 {
		return nil
	}
	kek, err := hkdf.Key(sha256.New, clusterSecret, nil, kekInfo, kekLen)
	if err != nil {
		return nil
	}
	return kek
}

// newAEAD returns AES-256-GCM for kek.
func newAEAD(kek []byte) (cipher.AEAD, error) {
	if len(kek) != kekLen {
		return nil, errors.New("iam: key-encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// seal encrypts plaintext with a fresh random nonce and authenticates aad.
// The result is nonce || ciphertext.
func seal(aead cipher.AEAD, aad, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), nil
}

// open reverses seal. It fails when the value was tampered with or when aad
// differs from the value sealed.
func open(aead cipher.AEAD, aad, sealed []byte) ([]byte, error) {
	n := aead.NonceSize()
	if len(sealed) < n {
		return nil, errSealedTooShort
	}
	return aead.Open(nil, sealed[:n], sealed[n:], aad)
}
