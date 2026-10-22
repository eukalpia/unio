package metatest

import (
	"testing"

	"github.com/eukalpia/unio/internal/meta"
)

// TestFakeStoreSuite runs the store conformance suite against the Fake, which is
// the reference implementation of the semantics.
func TestFakeStoreSuite(t *testing.T) {
	RunStoreSuite(t, func(t *testing.T) meta.Store { return NewFake() })
}

// TestFakeSystemKVSuite runs the SystemKV conformance suite against the Fake.
func TestFakeSystemKVSuite(t *testing.T) {
	RunSystemKVSuite(t, func(t *testing.T) meta.SystemKV { return NewFake() })
}
