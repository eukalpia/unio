package sigv4

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
)

// The example secret and derived key come from the AWS SigV4 documentation.
const docSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"

func TestDeriveSigningKeyAWSExample(t *testing.T) {
	key := deriveSigningKey(docSecret, "20120215", "us-east-1", "iam")
	want := "f4780e2d9f65fa895f9c67b32ce1baf0b0d8a43505a000a1a9e090d414db404d"
	if got := hex.EncodeToString(key[:]); got != want {
		t.Fatalf("signing key = %s, want %s", got, want)
	}
}

func TestSigningKeyDependsOnEveryInput(t *testing.T) {
	base := deriveSigningKey(docSecret, "20120215", "us-east-1", service)
	variants := []struct {
		name string
		key  [sha256.Size]byte
	}{
		{"secret", deriveSigningKey(docSecret+"x", "20120215", "us-east-1", service)},
		{"date", deriveSigningKey(docSecret, "20120216", "us-east-1", service)},
		{"region", deriveSigningKey(docSecret, "20120215", "eu-west-1", service)},
		{"service", deriveSigningKey(docSecret, "20120215", "us-east-1", "iam")},
	}
	for _, v := range variants {
		if v.key == base {
			t.Fatalf("changing %s did not change the signing key", v.name)
		}
	}
}

func TestSigningKeyCacheReturnsDerivedKey(t *testing.T) {
	a := New(nil, Options{Region: "us-east-1"})
	want := deriveSigningKey(docSecret, "20120215", "us-east-1", service)
	for i := range 3 {
		if got := a.signingKey(docSecret, "20120215"); got != want {
			t.Fatalf("call %d: cached key differs from derived key", i)
		}
	}
}

func TestKeyCacheIsBounded(t *testing.T) {
	const size = 8
	c := newKeyCache(size)
	for i := range 10000 {
		k := keyCacheKey{date: fmt.Sprint(i), region: "r"}
		c.put(k, [sha256.Size]byte{byte(i)})
		if n := len(c.cur) + len(c.prev); n > 2*size {
			t.Fatalf("cache holds %d entries after %d puts, bound is %d", n, i+1, 2*size)
		}
	}
}

func TestKeyCacheKeepsRecentlyUsedEntries(t *testing.T) {
	c := newKeyCache(4)
	hot := keyCacheKey{date: "hot"}
	c.put(hot, [sha256.Size]byte{1})
	for i := range 100 {
		c.put(keyCacheKey{date: fmt.Sprint(i)}, [sha256.Size]byte{})
		if _, ok := c.get(hot); !ok {
			t.Fatalf("hot entry evicted after %d inserts", i+1)
		}
	}
}

func TestKeyCacheMissAndPromotion(t *testing.T) {
	c := newKeyCache(2)
	a, b, d := keyCacheKey{date: "a"}, keyCacheKey{date: "b"}, keyCacheKey{date: "d"}
	c.put(a, [sha256.Size]byte{1})
	c.put(b, [sha256.Size]byte{2})
	c.put(d, [sha256.Size]byte{3}) // rotates: a and b move to prev
	if v, ok := c.get(a); !ok || v[0] != 1 {
		t.Fatal("entry from previous generation not found")
	}
	if _, ok := c.get(keyCacheKey{date: "missing"}); ok {
		t.Fatal("unexpected hit for an absent key")
	}
}
