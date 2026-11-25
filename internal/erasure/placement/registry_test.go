package placement

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/drive/drivetest"
	"github.com/eukalpia/unio/internal/drive/offline"
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// TestNewOrdersSetsByID checks that sets are built in set-id order even when the
// map lists them in another order, and that Get rejects unknown ids.
func TestNewOrdersSetsByID(t *testing.T) {
	f := newFixture(3, 2, 1, nil)
	slices.Reverse(f.m.Sets)
	r := f.registry(t, Options{})

	for i, s := range r.Sets() {
		if s.ID() != ids.SetID(i+1) {
			t.Fatalf("Sets()[%d].ID() = %d, want %d", i, s.ID(), i+1)
		}
	}
	for id := ids.SetID(1); id <= 3; id++ {
		s, err := r.Get(id)
		if err != nil {
			t.Fatalf("Get(%d): %v", id, err)
		}
		if s.ID() != id {
			t.Fatalf("Get(%d) returned set %d", id, s.ID())
		}
	}
	if _, err := r.Get(9); !errors.Is(err, erasure.ErrUnknownSet) {
		t.Fatalf("Get(9): got %v, want erasure.ErrUnknownSet", err)
	}
}

// TestNewRejectsInvalidInput covers every error path of New.
func TestNewRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		run  func() error
	}{
		{"nil map", func() error {
			_, err := New(nil, mapSource{}, Options{})
			return err
		}},
		{"nil drive source", func() error {
			_, err := New(newMap(1, 2, 1), nil, Options{})
			return err
		}},
		{"no sets", func() error {
			_, err := New(&cluster.Map{}, mapSource{}, Options{})
			return err
		}},
		{"duplicate set id", func() error {
			m := newMap(2, 2, 1)
			m.Sets[1].ID = m.Sets[0].ID
			_, err := New(m, newFixture(2, 2, 1, nil).src, Options{})
			return err
		}},
		{"drive without a location", func() error {
			m := newMap(1, 2, 1)
			m.Drives = m.Drives[1:]
			_, err := New(m, newFixture(1, 2, 1, nil).src, Options{})
			return err
		}},
		{"full ratio above one", func() error {
			_, err := New(newMap(1, 2, 1), newFixture(1, 2, 1, nil).src, Options{FullRatio: 1.5})
			return err
		}},
		{"negative full ratio", func() error {
			_, err := New(newMap(1, 2, 1), newFixture(1, 2, 1, nil).src, Options{FullRatio: -0.1})
			return err
		}},
		{"NaN full ratio", func() error {
			_, err := New(newMap(1, 2, 1), newFixture(1, 2, 1, nil).src, Options{FullRatio: math.NaN()})
			return err
		}},
		{"negative refresh interval", func() error {
			_, err := New(newMap(1, 2, 1), newFixture(1, 2, 1, nil).src, Options{RefreshInterval: -1})
			return err
		}},
		{"budget below one stripe frame", func() error {
			_, err := New(newMap(1, 2, 1), newFixture(1, 2, 1, nil).src, Options{Budget: bufpool.NewBudget(1)})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("New accepted invalid input")
			}
		})
	}
}

// TestNewServesFailingDriveOffline removes one drive from the source. The slot
// must hold an offline placeholder with the drive's id and location, the rest
// of the set must stay real, and the failure must be logged once at Warn.
func TestNewServesFailingDriveOffline(t *testing.T) {
	f := newFixture(2, 2, 1, nil)
	bad := f.m.Sets[1].Drives[2]
	delete(f.src, bad)
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := f.registry(t, Options{Logger: logger})

	slots := r.drives[1]
	od, ok := slots[2].(*offline.Drive)
	if !ok {
		t.Fatalf("slot 2 is %T, want *offline.Drive", slots[2])
	}
	info, err := od.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.ID != bad || info.Node != "node-2" || info.Endpoint != "/data/set2/slot2" || info.Online {
		t.Fatalf("placeholder info = %+v", info)
	}
	for slot, d := range slots {
		if _, isOffline := d.(*offline.Drive); isOffline && slot != 2 {
			t.Fatalf("slot %d is offline, want only slot 2", slot)
		}
	}
	if n := strings.Count(logs.String(), "erasure drive unavailable"); n != 1 {
		t.Fatalf("logged %d unavailable-drive warnings, want 1:\n%s", n, logs.String())
	}
	if !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("warning not logged at WARN:\n%s", logs.String())
	}
}

// TestNewReplacesMismatchedDrive checks that a drive source returning another
// drive for a slot is not trusted: the slot becomes an offline placeholder.
func TestNewReplacesMismatchedDrive(t *testing.T) {
	f := newFixture(1, 2, 1, nil)
	slot := f.m.Sets[0].Drives[0]
	f.src[slot] = drivetest.NewMemory(ids.NewDriveID())
	r := f.registry(t, Options{})
	if _, ok := r.drives[0][0].(*offline.Drive); !ok {
		t.Fatalf("slot 0 is %T, want *offline.Drive", r.drives[0][0])
	}
}

// TestSetsUseOptions checks that the stripe size of Options reaches the sets:
// the layout of a write records it.
func TestSetsUseOptions(t *testing.T) {
	f := newFixture(2, 2, 1, nil)
	r := f.registry(t, Options{StripeSize: 4096})
	ctx := context.Background()
	s, err := r.Place(ctx, "b", "k", 10000)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	layout, err := s.Write(ctx, ids.NewDataID(), bytes.NewReader(make([]byte, 10000)), 10000)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if layout.StripeSize != 4096 {
		t.Fatalf("layout stripe size %d, want 4096", layout.StripeSize)
	}
}

// TestPlaceWithOfflineDriveStillWrites checks that a set with one offline drive
// still takes a write: 2 of its 3 shards reach quorum, and the offline slot is
// not recorded.
func TestPlaceWithOfflineDriveStillWrites(t *testing.T) {
	f := newFixture(1, 2, 1, nil)
	delete(f.src, f.m.Sets[0].Drives[0])
	r := f.registry(t, Options{})
	ctx := context.Background()
	s, err := r.Place(ctx, "b", "k", 5000)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}
	layout, err := s.Write(ctx, ids.NewDataID(), bytes.NewReader(make([]byte, 5000)), 5000)
	if err != nil {
		t.Fatalf("Write with one offline drive: %v", err)
	}
	if !layout.Drives[0].IsZero() || layout.Written() != 2 {
		t.Fatalf("layout drives %v: want slot 0 unwritten and 2 shards written", layout.Drives)
	}
}
