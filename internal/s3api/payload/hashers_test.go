package payload

import (
	"testing"

	"github.com/eukalpia/unio/internal/object"
)

func TestDigestLengthsMatchHashers(t *testing.T) {
	for _, a := range algorithms() {
		n, ok := digestLength(a)
		if !ok {
			t.Fatalf("%s is not a known algorithm", a)
		}
		h := newHash(a)
		if h == nil {
			t.Fatalf("no hasher for %s", a)
		}
		if h.Size() != n || n != len(checkDigest(a)) {
			t.Errorf("%s: table %d, hasher %d, published digest %d bytes", a, n, h.Size(), len(checkDigest(a)))
		}
	}
}

func TestUnknownAlgorithms(t *testing.T) {
	if n, ok := digestLength(object.ChecksumNone); !ok || n != 0 {
		t.Fatalf("ChecksumNone: length %d, known %v; want 0, true", n, ok)
	}
	if newHash(object.ChecksumNone) != nil {
		t.Fatal("ChecksumNone must not have a hasher")
	}
	for _, a := range []object.ChecksumAlgorithm{6, 200} {
		if _, ok := digestLength(a); ok {
			t.Fatalf("algorithm %d reported as known", a)
		}
		if newHash(a) != nil {
			t.Fatalf("algorithm %d has a hasher", a)
		}
	}
}
