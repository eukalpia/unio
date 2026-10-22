package keys

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestNewKeyspaceRejectsEmpty(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "zero keys", n: 0},
		{name: "negative keys", n: -3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewKeyspace("bench/4k/", tc.n); err == nil {
				t.Fatalf("NewKeyspace(n=%d) returned no error", tc.n)
			}
		})
	}
}

func TestKeyspaceKeyFormat(t *testing.T) {
	ks, err := NewKeyspace("bench/4k/", 1000)
	if err != nil {
		t.Fatalf("NewKeyspace: %v", err)
	}
	tests := []struct {
		i    int
		want string
	}{
		{i: 0, want: "bench/4k/00000000"},
		{i: 7, want: "bench/4k/00000007"},
		{i: 999, want: "bench/4k/00000999"},
		{i: 1000, want: "bench/4k/00000000"},  // wraps into the keyspace
		{i: -1, want: "bench/4k/00000999"},    // negative indices wrap too
		{i: -1000, want: "bench/4k/00000000"}, // exact negative multiple
	}
	for _, tc := range tests {
		if got := ks.Key(tc.i); got != tc.want {
			t.Errorf("Key(%d) = %q, want %q", tc.i, got, tc.want)
		}
	}
}

func TestKeyspaceKeysAreUnique(t *testing.T) {
	ks, err := NewKeyspace("bench/1m/", 500)
	if err != nil {
		t.Fatalf("NewKeyspace: %v", err)
	}
	seen := make(map[string]struct{}, ks.Len())
	for i := 0; i < ks.Len(); i++ {
		k := ks.Key(i)
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate key %q at index %d", k, i)
		}
		seen[k] = struct{}{}
		if !strings.HasPrefix(k, "bench/1m/") {
			t.Fatalf("key %q lacks prefix", k)
		}
	}
}

func TestKeyspacePickCoversRangeDeterministically(t *testing.T) {
	const n = 10
	ks, err := NewKeyspace("p/", n)
	if err != nil {
		t.Fatalf("NewKeyspace: %v", err)
	}
	draw := func(seed uint64) []string {
		rng := rand.New(rand.NewPCG(seed, 0))
		out := make([]string, 0, 1000)
		for i := 0; i < 1000; i++ {
			out = append(out, ks.Pick(rng))
		}
		return out
	}
	a, b := draw(42), draw(42)
	seen := make(map[string]bool)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("draw %d differs between identical seeds: %q vs %q", i, a[i], b[i])
		}
		seen[a[i]] = true
	}
	if len(seen) != n {
		t.Fatalf("1000 draws covered %d of %d keys", len(seen), n)
	}
}

func TestNewListLayoutRejectsEmpty(t *testing.T) {
	if _, err := NewListLayout(0); err == nil {
		t.Fatal("NewListLayout(0) returned no error")
	}
}

func TestListLayoutPrefixCount(t *testing.T) {
	tests := []struct {
		keys, want int
	}{
		{keys: 1, want: 1},
		{keys: 2, want: 2},
		{keys: 3, want: 3},
		{keys: 4, want: 4},
		{keys: 10000, want: 4},
	}
	for _, tc := range tests {
		l, err := NewListLayout(tc.keys)
		if err != nil {
			t.Fatalf("NewListLayout(%d): %v", tc.keys, err)
		}
		if l.Prefixes != tc.want {
			t.Errorf("keys=%d: Prefixes = %d, want %d", tc.keys, l.Prefixes, tc.want)
		}
	}
}

func TestListLayoutKeys(t *testing.T) {
	const total = 1000
	l, err := NewListLayout(total)
	if err != nil {
		t.Fatalf("NewListLayout: %v", err)
	}
	if got := l.Prefix(1); got != "list/p01/" {
		t.Fatalf("Prefix(1) = %q, want list/p01/", got)
	}

	seen := make(map[string]struct{}, total)
	perPrefix := make([]int, l.Prefixes)
	archive := 0
	for i := 0; i < total; i++ {
		k := l.Key(i)
		if _, dup := seen[k]; dup {
			t.Fatalf("duplicate key %q", k)
		}
		seen[k] = struct{}{}
		p := i % l.Prefixes
		if !strings.HasPrefix(k, l.Prefix(p)) {
			t.Fatalf("key %q is not under prefix %q", k, l.Prefix(p))
		}
		perPrefix[p]++
		if strings.Contains(k, "/archive/") {
			archive++
		}
	}
	sum := 0
	for _, c := range perPrefix {
		sum += c
	}
	if sum != total {
		t.Fatalf("keys across prefixes = %d, want %d", sum, total)
	}
	// Every tenth key within a prefix goes to the archive sub-prefix, so the
	// delimiter variant of LIST sees a non-trivial common prefix.
	if want := total / 10; archive != want {
		t.Fatalf("archive keys = %d, want %d", archive, want)
	}
}
