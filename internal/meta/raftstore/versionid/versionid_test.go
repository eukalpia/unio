package versionid

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func TestNewLength(t *testing.T) {
	t.Parallel()
	if got := New(); len(got) != 32 {
		t.Fatalf("New() = %q, want 32 hex digits", got)
	}
}

func TestNewIsLowercaseHex(t *testing.T) {
	t.Parallel()
	id := New()
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("New() = %q is not hex: %v", id, err)
	}
	if strings.ToLower(id) != id {
		t.Fatalf("New() = %q is not lower case", id)
	}
}

func TestNewEmbedsTimestamp(t *testing.T) {
	t.Parallel()
	before := time.Now().UnixNano()
	id := New()
	after := time.Now().UnixNano()

	raw, err := hex.DecodeString(id)
	if err != nil {
		t.Fatal(err)
	}
	ts := int64(binary.BigEndian.Uint64(raw[:8]))
	if ts < before || ts > after {
		t.Fatalf("timestamp prefix %d outside [%d, %d]", ts, before, after)
	}
}

func TestNewIsTimeOrdered(t *testing.T) {
	t.Parallel()
	first := New()
	second := New()
	if first[:16] > second[:16] {
		t.Fatalf("time prefix not ordered: %s then %s", first, second)
	}
}

func TestNewIsUnique(t *testing.T) {
	t.Parallel()
	const n = 10000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		id := New()
		if id == "" {
			t.Fatal("New() returned empty string")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate version id %s after %d calls", id, i)
		}
		seen[id] = struct{}{}
	}
}
