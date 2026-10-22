package segment

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeSegment writes the given payloads as records and returns the file path,
// the record offsets and the total committed length.
func writeSegment(t *testing.T, payloads ...[]byte) (string, []int64, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "seg")
	f, err := Create(path, 1<<16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer f.Close()
	var offs []int64
	for i, p := range payloads {
		off, err := f.Append(AppendRecord(nil, 0, testID(uint16(i+1)), p))
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		offs = append(offs, off)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return path, offs, f.End()
}

func scanFile(t *testing.T, path string, verify bool) ([]Record, int64, error) {
	t.Helper()
	f, err := Open(path, 1<<16, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	size, err := f.Size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	var recs []Record
	end, err := Scan(f, size, verify, func(r Record) error {
		if verify {
			r.Data = append([]byte(nil), r.Data...)
		}
		recs = append(recs, r)
		return nil
	})
	return recs, end, err
}

func TestScanCompleteSegment(t *testing.T) {
	payloads := [][]byte{[]byte("a"), bytes.Repeat([]byte("b"), 300), {}}
	path, offs, total := writeSegment(t, payloads...)
	for _, verify := range []bool{false, true} {
		recs, end, err := scanFile(t, path, verify)
		if err != nil {
			t.Fatalf("verify=%v: scan: %v", verify, err)
		}
		if end != total || len(recs) != len(payloads) {
			t.Fatalf("verify=%v: end %d (want %d), %d records", verify, end, total, len(recs))
		}
		for i, r := range recs {
			if r.Offset != offs[i] || r.Header.ShardID != testID(uint16(i+1)) {
				t.Fatalf("verify=%v: record %d at %d: %+v", verify, i, r.Offset, r.Header)
			}
			if verify && !bytes.Equal(r.Data, payloads[i]) {
				t.Fatalf("verify=%v: record %d data mismatch", verify, i)
			}
		}
	}
}

func TestScanStopsAtTornTail(t *testing.T) {
	cuts := []struct {
		name   string
		remove int64 // bytes cut from the end of the file
	}{
		{"inside-data", 5},
		{"inside-header", RecordSize(int64(len("second-record"))) - 10}, // 10 header bytes survive
		{"one-byte", 1},
	}
	for _, tc := range cuts {
		t.Run(tc.name, func(t *testing.T) {
			path, offs, total := writeSegment(t, []byte("first"), []byte("second-record"))
			if err := os.Truncate(path, total-tc.remove); err != nil {
				t.Fatalf("truncate: %v", err)
			}
			recs, end, err := scanFile(t, path, true)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if len(recs) != 1 || end != offs[1] {
				t.Fatalf("got %d records, end %d; want 1 record, end %d", len(recs), end, offs[1])
			}
		})
	}
}

func TestScanStopsAtCorruptData(t *testing.T) {
	path, offs, _ := writeSegment(t, []byte("keep"), []byte("damaged"), []byte("unreached"))
	raw, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := raw.WriteAt([]byte{'Z'}, offs[1]+HeaderSize); err != nil {
		t.Fatalf("corrupt: %v", err)
	}
	raw.Close()

	recs, end, err := scanFile(t, path, true)
	if err != nil || len(recs) != 1 || end != offs[1] {
		t.Fatalf("verified scan: %d records, end %d, err %v; want 1, %d", len(recs), end, err, offs[1])
	}
	// Header-only scans cannot see data corruption; they still find all records.
	recs, _, err = scanFile(t, path, false)
	if err != nil || len(recs) != 3 {
		t.Fatalf("header-only scan: %d records, err %v; want 3", len(recs), err)
	}
}

func TestScanStopsAtZeroedPreallocation(t *testing.T) {
	path, _, total := writeSegment(t, []byte("only"))
	if err := os.Truncate(path, total+4096); err != nil {
		t.Fatalf("extend: %v", err)
	}
	recs, end, err := scanFile(t, path, true)
	if err != nil || len(recs) != 1 || end != total {
		t.Fatalf("got %d records, end %d, err %v; want 1, %d", len(recs), end, err, total)
	}
}

func TestScanPropagatesCallbackError(t *testing.T) {
	path, _, _ := writeSegment(t, []byte("x"))
	f, err := Open(path, 1<<16, 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	size, err := f.Size()
	if err != nil {
		t.Fatalf("size: %v", err)
	}
	sentinel := errors.New("stop here")
	_, err = Scan(f, size, false, func(Record) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want sentinel", err)
	}
}
