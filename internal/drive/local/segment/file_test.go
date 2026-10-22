package segment

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileAppendAndReadRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	f, err := Create(path, 4096)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()

	first := []byte("first shard")
	second := bytes.Repeat([]byte{0xab}, 100)
	off1, err := f.Append(AppendRecord(nil, 0, testID(1), first))
	if err != nil || off1 != 0 {
		t.Fatalf("append 1: off %d, err %v", off1, err)
	}
	off2, err := f.Append(AppendRecord(nil, 0, testID(2), second))
	if err != nil || off2 != RecordSize(int64(len(first))) {
		t.Fatalf("append 2: off %d, err %v", off2, err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}

	buf := make([]byte, 4096)
	h, rec, err := f.ReadRecord(off2, buf)
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if h.ShardID != testID(2) || !bytes.Equal(rec[HeaderSize:], second) {
		t.Fatalf("record 2 mismatch: %+v", h)
	}
	hdr, err := f.ReadHeader(off1)
	if err != nil || hdr.ShardID != testID(1) || hdr.Length != uint64(len(first)) {
		t.Fatalf("read header: %+v, %v", hdr, err)
	}
}

func TestFileFullAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	f, err := Create(path, 128)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rec := AppendRecord(nil, 0, testID(1), make([]byte, 60)) // 102 bytes
	if _, err := f.Append(rec); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := f.Append(rec); !errors.Is(err, ErrFull) {
		t.Fatalf("overflow: err = %v, want ErrFull", err)
	}
	if f.End() != int64(len(rec)) {
		t.Fatalf("end = %d after rejected append", f.End())
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	g, err := Open(path, 128, int64(len(rec)))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer g.Close()
	if g.End() != int64(len(rec)) {
		t.Fatalf("reopened end = %d, want %d", g.End(), len(rec))
	}
	if _, err := g.Append(rec); !errors.Is(err, ErrFull) {
		t.Fatalf("append past capacity: err = %v, want ErrFull", err)
	}
	g.SetEnd(0)
	if _, err := g.Append(rec); err != nil {
		t.Fatalf("append after SetEnd: %v", err)
	}
}

func TestFileReadRecordDetectsCorruption(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	f, err := Create(path, 1024)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if _, err := f.Append(AppendRecord(nil, 0, testID(5), []byte("payload"))); err != nil {
		t.Fatalf("append: %v", err)
	}
	// Flip one data byte behind the File's back.
	raw, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.WriteAt([]byte{'X'}, HeaderSize); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	raw.Close()

	if _, _, err := f.ReadRecord(0, make([]byte, 256)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestFileTruncateAndSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	f, err := Create(path, 1<<12)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	if err := f.Truncate(10); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	size, err := f.Size()
	if err != nil || size != 10 {
		t.Fatalf("size = %d, %v; want 10", size, err)
	}
}
