package remotedrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

// runDriveSuite checks the drive.Drive contract against drives returned by mk.
// mk must return an empty drive. The suite is a local stand-in for the shared
// drivetest suite: it covers the rules this package relies on.
func runDriveSuite(t *testing.T, mk func(t *testing.T) drive.Drive) {
	t.Helper()
	fresh := func(t *testing.T) (drive.Drive, context.Context) {
		t.Helper()
		d := mk(t)
		t.Cleanup(func() { _ = d.Close() })
		return d, context.Background()
	}
	newShard := func(i uint16) ids.ShardID {
		return ids.ShardID{Data: ids.NewDataID(), Index: i}
	}

	t.Run("write then read", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(0)
		want := []byte("shard payload")
		if err := d.WriteShard(ctx, id, bytes.NewReader(want), int64(len(want))); err != nil {
			t.Fatal(err)
		}
		rc, err := d.ReadShard(ctx, id, 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("read %q, want %q", got, want)
		}
	})

	t.Run("ranged reads", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(1)
		data := []byte("0123456789")
		if err := d.WriteShard(ctx, id, bytes.NewReader(data), int64(len(data))); err != nil {
			t.Fatal(err)
		}
		cases := []struct {
			name           string
			offset, length int64
			want           string
		}{
			{name: "middle", offset: 3, length: 4, want: "3456"},
			{name: "to end", offset: 6, length: -1, want: "6789"},
			{name: "zero length", offset: 5, length: 0, want: ""},
			{name: "offset at end", offset: 10, length: -1, want: ""},
		}
		for _, tc := range cases {
			rc, err := d.ReadShard(ctx, id, tc.offset, tc.length)
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			got, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if string(got) != tc.want {
				t.Fatalf("%s: got %q, want %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("read out of range fails", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(2)
		if err := d.WriteShard(ctx, id, bytes.NewReader([]byte("abc")), 3); err != nil {
			t.Fatal(err)
		}
		if rc, err := d.ReadShard(ctx, id, 1, 10); err == nil {
			_ = rc.Close()
			t.Fatal("range beyond the shard accepted")
		}
		if rc, err := d.ReadShard(ctx, id, 4, -1); err == nil {
			_ = rc.Close()
			t.Fatal("offset beyond the shard accepted")
		}
	})

	t.Run("shards are immutable", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(3)
		if err := d.WriteShard(ctx, id, bytes.NewReader([]byte("a")), 1); err != nil {
			t.Fatal(err)
		}
		err := d.WriteShard(ctx, id, bytes.NewReader([]byte("b")), 1)
		if !errors.Is(err, drive.ErrShardExists) {
			t.Fatalf("second write error = %v, want ErrShardExists", err)
		}
	})

	t.Run("missing shard errors", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(4)
		if _, err := d.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("stat: %v", err)
		}
		if rc, err := d.ReadShard(ctx, id, 0, -1); !errors.Is(err, drive.ErrShardNotFound) {
			if err == nil {
				_ = rc.Close()
			}
			t.Fatalf("read: %v", err)
		}
		if err := d.DeleteShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("delete: %v", err)
		}
	})

	t.Run("stat reports size", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(5)
		if err := d.WriteShard(ctx, id, bytes.NewReader(make([]byte, 77)), 77); err != nil {
			t.Fatal(err)
		}
		info, err := d.StatShard(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if info.ID != id || info.Size != 77 {
			t.Fatalf("stat = %+v", info)
		}
	})

	t.Run("delete removes", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(6)
		if err := d.WriteShard(ctx, id, bytes.NewReader([]byte("x")), 1); err != nil {
			t.Fatal(err)
		}
		if err := d.DeleteShard(ctx, id); err != nil {
			t.Fatal(err)
		}
		if _, err := d.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("stat after delete: %v", err)
		}
	})

	t.Run("short source stores nothing", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(7)
		if err := d.WriteShard(ctx, id, bytes.NewReader([]byte("abc")), 10); err == nil {
			t.Fatal("short write succeeded")
		}
		if _, err := d.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("short write left a shard behind: %v", err)
		}
	})

	t.Run("long source stores nothing", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(8)
		if err := d.WriteShard(ctx, id, bytes.NewReader(make([]byte, 20)), 10); err == nil {
			t.Fatal("long write succeeded")
		}
		if _, err := d.StatShard(ctx, id); !errors.Is(err, drive.ErrShardNotFound) {
			t.Fatalf("long write left a shard behind: %v", err)
		}
	})

	t.Run("zero-length shard", func(t *testing.T) {
		d, ctx := fresh(t)
		id := newShard(9)
		if err := d.WriteShard(ctx, id, bytes.NewReader(nil), 0); err != nil {
			t.Fatal(err)
		}
		info, err := d.StatShard(ctx, id)
		if err != nil || info.Size != 0 {
			t.Fatalf("stat = %+v, %v", info, err)
		}
	})

	t.Run("walk visits each shard once", func(t *testing.T) {
		d, ctx := fresh(t)
		want := map[ids.ShardID]bool{}
		for i := range 700 {
			id := newShard(uint16(i % 9))
			id.Data = ids.NewDataID()
			if err := d.WriteShard(ctx, id, bytes.NewReader([]byte{byte(i)}), 1); err != nil {
				t.Fatal(err)
			}
			want[id] = true
		}
		seen := map[ids.ShardID]int{}
		if err := d.WalkShards(ctx, func(info drive.ShardInfo) error {
			seen[info.ID]++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(seen) != len(want) {
			t.Fatalf("walk saw %d shards, want %d", len(seen), len(want))
		}
		for id, n := range seen {
			if n != 1 || !want[id] {
				t.Fatalf("shard %s seen %d times (known: %v)", id, n, want[id])
			}
		}
	})

	t.Run("walk stops at callback error", func(t *testing.T) {
		d, ctx := fresh(t)
		for i := range 5 {
			id := newShard(uint16(i))
			if err := d.WriteShard(ctx, id, bytes.NewReader([]byte{1}), 1); err != nil {
				t.Fatal(err)
			}
		}
		stop := errors.New("stop here")
		calls := 0
		err := d.WalkShards(ctx, func(drive.ShardInfo) error {
			calls++
			return stop
		})
		if !errors.Is(err, stop) || calls != 1 {
			t.Fatalf("walk error = %v after %d calls", err, calls)
		}
	})

	t.Run("info reports identity", func(t *testing.T) {
		d, ctx := fresh(t)
		info, err := d.Info(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if info.ID != d.ID() || !info.Online {
			t.Fatalf("info = %+v", info)
		}
	})

	t.Run("canceled context fails", func(t *testing.T) {
		d, _ := fresh(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		id := newShard(10)
		if err := d.WriteShard(ctx, id, bytes.NewReader([]byte("x")), 1); err == nil {
			t.Fatal("write with canceled context succeeded")
		}
		if _, err := d.StatShard(ctx, id); err == nil {
			t.Fatal("stat with canceled context succeeded")
		}
	})

	t.Run("calls after close fail", func(t *testing.T) {
		d, ctx := fresh(t)
		if err := d.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := d.StatShard(ctx, newShard(11)); !errors.Is(err, drive.ErrClosed) {
			t.Fatalf("stat after close: %v", err)
		}
	})
}
