package logstore

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"
	"github.com/hashicorp/raft"
)

// TestEncodeRejectsAppendTimeBeforeEpoch checks that an append time before the
// Unix epoch is refused rather than stored. Raft stamps entries with the
// current time, so such a value cannot come from Raft. Keeping the stored
// seconds unsigned means that a corrupt sign bit can never decode into a time.
func TestEncodeRejectsAppendTimeBeforeEpoch(t *testing.T) {
	for _, at := range []time.Time{
		time.Unix(-1, 0),
		time.Date(1931, 3, 4, 5, 6, 7, 1, time.UTC),
	} {
		if _, err := encodeLog(&raft.Log{Index: 1, Term: 1, AppendedAt: at}); err == nil {
			t.Fatalf("encodeLog accepted append time %v before the epoch", at)
		}
	}
	if _, err := encodeLog(&raft.Log{Index: 1, Term: 1, AppendedAt: time.Unix(0, 0)}); err != nil {
		t.Fatalf("encodeLog rejected the epoch itself: %v", err)
	}
}

// TestUint32FieldRejectsOutOfRange checks the explicit bounds check that runs
// before every conversion to a 4-byte length or nanosecond field.
func TestUint32FieldRejectsOutOfRange(t *testing.T) {
	for _, n := range []int{-1, math.MinInt32, -1 << 40} {
		if _, err := uint32Field(n); err == nil {
			t.Fatalf("uint32Field(%d) accepted a negative value", n)
		}
	}
	for _, n := range []int{0, 1, 999_999_999} {
		got, err := uint32Field(n)
		if err != nil {
			t.Fatalf("uint32Field(%d): %v", n, err)
		}
		if int(got) != n {
			t.Fatalf("uint32Field(%d) = %d", n, got)
		}
	}
	if strconv.IntSize == 64 {
		if _, err := uint32Field(1 << 32); err == nil {
			t.Fatal("uint32Field accepted 2^32")
		}
		if got, err := uint32Field(math.MaxUint32); err != nil || got != math.MaxUint32 {
			t.Fatalf("uint32Field(2^32-1) = %d, %v", got, err)
		}
	}
}

// TestCorruptStableValueIsReportedAsCorrupt checks that a stable value of the
// wrong size is reported with ErrCorrupt.
func TestCorruptStableValueIsReportedAsCorrupt(t *testing.T) {
	g := newTestStore(t).Group(3)
	if err := g.Set([]byte("term"), []byte{1, 2, 3}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if _, err := g.GetUint64([]byte("term")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetUint64 of a 3 byte value: error %v, want ErrCorrupt", err)
	}
}

// TestCorruptEntryIsReportedAsCorrupt damages a stored entry in place and
// checks that GetLog reports it with ErrCorrupt instead of returning a partial
// log.
func TestCorruptEntryIsReportedAsCorrupt(t *testing.T) {
	s := newTestStore(t)
	g := s.Group(5)
	at := time.Unix(1_700_000_000, 5).UTC()
	if err := g.StoreLog(&raft.Log{Index: 1, Term: 1, Data: []byte("ok"), AppendedAt: at}); err != nil {
		t.Fatalf("StoreLog: %v", err)
	}
	value, closer, err := s.db.Get(logKey(5, 1))
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	damaged := append([]byte(nil), value...)
	if err := closer.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	const offSec = 19
	damaged[offSec] |= 0x80 // the sign bit of the append seconds
	if err := s.db.Set(logKey(5, 1), damaged, pebble.Sync); err != nil {
		t.Fatalf("write damaged entry: %v", err)
	}
	var out raft.Log
	if err := g.GetLog(1, &out); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("GetLog of a damaged entry: error %v, want ErrCorrupt", err)
	}
}
