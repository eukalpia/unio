package erasuretest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
)

var testShard = ids.ShardID{Data: ids.DataID{1}, Index: 3}

func TestDriveWriteReadRoundTrip(t *testing.T) {
	d := NewDrive()
	payload := []byte("0123456789")
	if err := d.WriteShard(context.Background(), testShard, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("WriteShard: %v", err)
	}
	rc, err := d.ReadShard(context.Background(), testShard, 2, 5)
	if err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	got, _ := io.ReadAll(rc)
	if string(got) != "23456" {
		t.Fatalf("range read = %q, want %q", got, "23456")
	}
	if err := d.WriteShard(context.Background(), testShard, bytes.NewReader(payload), 10); !errors.Is(err, drive.ErrShardExists) {
		t.Fatalf("second write: err = %v, want ErrShardExists", err)
	}
}

func TestDriveRejectsSizeMismatchAndStoresNothing(t *testing.T) {
	tests := []struct {
		name string
		data string
		size int64
	}{
		{"short", "abc", 5},
		{"long", "abcdef", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDrive()
			if err := d.WriteShard(context.Background(), testShard, bytes.NewReader([]byte(tt.data)), tt.size); err == nil {
				t.Fatal("WriteShard accepted a size mismatch")
			}
			if d.ShardCount() != 0 {
				t.Fatal("a rejected shard was stored")
			}
		})
	}
}

func TestDriveFaults(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		faults    Faults
		wantWrite error
		wantRead  error
	}{
		{"offline fails every call", Faults{Offline: true}, drive.ErrOffline, drive.ErrOffline},
		{"write error leaves reads working", Faults{WriteErr: drive.ErrFaulty}, drive.ErrFaulty, nil},
		{"read error leaves writes working", Faults{ReadErr: drive.ErrFaulty}, nil, drive.ErrFaulty},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := NewDrive()
			if err := d.WriteShard(ctx, testShard, bytes.NewReader([]byte("x")), 1); err != nil {
				t.Fatal(err)
			}
			d.SetFaults(tt.faults)
			other := ids.ShardID{Index: 9}
			if err := d.WriteShard(ctx, other, bytes.NewReader([]byte("x")), 1); !errors.Is(err, tt.wantWrite) {
				t.Fatalf("WriteShard: err = %v, want %v", err, tt.wantWrite)
			}
			_, err := d.ReadShard(ctx, testShard, 0, -1)
			if !errors.Is(err, tt.wantRead) {
				t.Fatalf("ReadShard: err = %v, want %v", err, tt.wantRead)
			}
		})
	}
}

func TestDriveMidStreamWriteFailureStoresNothing(t *testing.T) {
	d := NewDrive()
	d.SetFaults(Faults{WriteErr: drive.ErrFaulty, WriteErrAfter: 4})
	payload := bytes.Repeat([]byte{1}, 100)
	err := d.WriteShard(context.Background(), testShard, bytes.NewReader(payload), int64(len(payload)))
	if !errors.Is(err, drive.ErrFaulty) {
		t.Fatalf("err = %v, want ErrFaulty", err)
	}
	if d.ShardCount() != 0 {
		t.Fatal("partial shard was stored")
	}
}

func TestDriveCorruptIsCopyOnWrite(t *testing.T) {
	d := NewDrive()
	payload := []byte("abcdef")
	if err := d.WriteShard(context.Background(), testShard, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	rc, err := d.ReadShard(context.Background(), testShard, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Corrupt(testShard, 0); err != nil {
		t.Fatalf("Corrupt: %v", err)
	}
	old, _ := io.ReadAll(rc)
	if string(old) != "abcdef" {
		t.Fatalf("open reader saw corruption: %q", old)
	}
	now, _ := d.Shard(testShard)
	if now[0] == 'a' {
		t.Fatal("Corrupt did not change the stored byte")
	}
}

func TestDriveHangHonoursContext(t *testing.T) {
	d := NewDrive()
	d.SetFaults(Faults{Hang: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.ReadShard(ctx, testShard, 0, -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("hung call: err = %v, want context.Canceled", err)
	}
}

func TestDriveCloseIsTerminal(t *testing.T) {
	d := NewDrive()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.StatShard(context.Background(), testShard); !errors.Is(err, drive.ErrClosed) {
		t.Fatalf("StatShard after Close: err = %v, want ErrClosed", err)
	}
}
