package sigv4

import (
	"crypto/sha256"
)

// deriveSigningKey computes the SigV4 signing key:
//
//	kDate    = HMAC("AWS4"+secret, date)
//	kRegion  = HMAC(kDate, region)
//	kService = HMAC(kRegion, service)
//	kSigning = HMAC(kService, "aws4_request")
func deriveSigningKey(secret, date, region, svc string) [sha256.Size]byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(date))
	kRegion := hmacSHA256(kDate[:], []byte(region))
	kService := hmacSHA256(kRegion[:], []byte(svc))
	return hmacSHA256(kService[:], []byte(terminator))
}

// signingKey returns the signing key for a secret and date, using the
// authenticator's bounded cache. Derivation costs four HMACs, which is
// worth caching for the hot path.
func (a *Authenticator) signingKey(secret, date string) [sha256.Size]byte {
	k := keyCacheKey{
		secretDigest: sha256.Sum256([]byte(secret)),
		date:         date,
		region:       a.region,
	}
	if v, ok := a.keys.get(k); ok {
		return v
	}
	v := deriveSigningKey(secret, date, a.region, service)
	a.keys.put(k, v)
	return v
}
