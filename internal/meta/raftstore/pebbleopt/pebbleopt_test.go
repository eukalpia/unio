package pebbleopt

import (
	"testing"

	"github.com/cockroachdb/pebble/v2"
)

func TestOptions(t *testing.T) {
	cache := pebble.NewCache(1 << 20)
	defer cache.Unref()
	opts := Options(cache)
	if opts.Cache != cache {
		t.Fatal("Options does not use the shared cache")
	}
	if _, ok := opts.Logger.(quietLogger); !ok {
		t.Fatalf("Logger = %T, want quietLogger", opts.Logger)
	}
	if opts.FS == nil {
		t.Fatal("FS is nil")
	}
	// Informational messages are dropped without touching the default logger.
	quietLogger{}.Infof("compaction %d", 1)
}

// TestOptionsOpen checks that the options open a working database, so a
// change to them that Pebble rejects fails here rather than at node start.
func TestOptionsOpen(t *testing.T) {
	cache := pebble.NewCache(1 << 20)
	defer cache.Unref()
	db, err := pebble.Open(t.TempDir(), Options(cache))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Set([]byte("k"), []byte("v"), pebble.Sync); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}
