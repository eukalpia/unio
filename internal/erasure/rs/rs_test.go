package rs

import (
	"testing"

	"go.uber.org/goleak"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/erasure/erasuretest"
	"github.com/eukalpia/unio/internal/ids"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestSetConformance runs the shared conformance suite against Set.
func TestSetConformance(t *testing.T) {
	erasuretest.RunSetSuite(t, func(t *testing.T, drives []drive.Drive, k, m int) erasure.Set {
		t.Helper()
		s, err := New(ids.SetID(1), drives, k, m, Options{})
		if err != nil {
			t.Fatalf("New(%d+%d): %v", k, m, err)
		}
		return s
	})
}
