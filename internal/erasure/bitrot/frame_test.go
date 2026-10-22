package bitrot

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
)

// testPos is the position of the shard written by most tests.
var testPos = Position{Data: ids.DataID{1, 2, 3}, Shard: 4, FirstBlock: 0}

// failingReader returns the bytes of data, then err.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// failingWriter accepts limit bytes and then fails.
type failingWriter struct {
	limit int
	err   error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		n := w.limit
		w.limit = 0
		return n, w.err
	}
	w.limit -= len(p)
	return len(p), nil
}

// framedAt frames blocks at position p, one block after another.
func framedAt(t *testing.T, p Position, blocks ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf, p)
	for _, b := range blocks {
		if err := w.WriteBlock(b); err != nil {
			t.Fatalf("WriteBlock: %v", err)
		}
	}
	return buf.Bytes()
}

func framed(t *testing.T, blocks ...[]byte) []byte {
	t.Helper()
	return framedAt(t, testPos, blocks...)
}

// readBlocks reads one block of each length in lens from stream, verifying
// them as the blocks at position p, and returns the first error.
func readBlocks(stream []byte, p Position, lens ...int) error {
	r := NewReader(bytes.NewReader(stream), p)
	for _, n := range lens {
		if err := r.ReadBlock(make([]byte, n)); err != nil {
			return err
		}
	}
	return r.Done()
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		blocks [][]byte
	}{
		{"single block", [][]byte{[]byte("hello world")}},
		{"full and short", [][]byte{bytes.Repeat([]byte{7}, 64), bytes.Repeat([]byte{9}, 64), []byte("tail")}},
		{"one byte blocks", [][]byte{{1}, {2}, {3}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := framed(t, tt.blocks...)
			r := NewReader(bytes.NewReader(stream), testPos)
			for i, want := range tt.blocks {
				got := make([]byte, len(want))
				if err := r.ReadBlock(got); err != nil {
					t.Fatalf("block %d: %v", i, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("block %d: got %x want %x", i, got, want)
				}
			}
			if err := r.Done(); err != nil {
				t.Fatalf("Done: %v", err)
			}
		})
	}
}

func TestFrameLayout(t *testing.T) {
	data := []byte("abc")
	stream := framed(t, data)
	if len(stream) != HashSize+len(data) {
		t.Fatalf("framed length %d, want %d", len(stream), HashSize+len(data))
	}
	sum := digestAt(t, testPos, 0, data)
	if !bytes.Equal(stream[:HashSize], sum[:]) || !bytes.Equal(stream[HashSize:], data) {
		t.Fatal("block is not [digest][data]")
	}
}

func TestReaderDetectsCorruptionAndStaysAligned(t *testing.T) {
	tests := []struct {
		name string
		flip int // byte index in the framed stream
	}{
		{"first data byte", HashSize},
		{"last data byte", HashSize + 15},
		{"first hash byte", 0},
		{"last hash byte of block one", HashSize - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b1 := bytes.Repeat([]byte{0xAB}, 16)
			b2 := bytes.Repeat([]byte{0xCD}, 16)
			stream := framed(t, b1, b2)
			stream[tt.flip] ^= 0x01
			r := NewReader(bytes.NewReader(stream), testPos)

			got := bytes.Repeat([]byte{0xFF}, 16)
			err := r.ReadBlock(got)
			if !errors.Is(err, erasure.ErrCorrupt) {
				t.Fatalf("corrupt block: err = %v, want ErrCorrupt", err)
			}
			if !bytes.Equal(got, make([]byte, 16)) {
				t.Fatalf("corrupt block returned bytes %x; must be zeroed", got)
			}
			// The stream is still aligned: the next block verifies.
			if err := r.ReadBlock(make([]byte, 16)); err != nil {
				t.Fatalf("block after corruption: %v", err)
			}
		})
	}
}

// TestSwappedBlocksAreDetected: each block verifies on its own, so only the
// position binding catches two valid blocks exchanged within one shard.
func TestSwappedBlocksAreDetected(t *testing.T) {
	a := bytes.Repeat([]byte{'a'}, 32)
	b := bytes.Repeat([]byte{'b'}, 32)
	fa := framedAt(t, Position{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: 0}, a)
	fb := framedAt(t, Position{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: 1}, b)
	if err := readBlocks(append(append([]byte{}, fa...), fb...), testPos, 32, 32); err != nil {
		t.Fatalf("pristine shard failed: %v", err)
	}
	swapped := append(append([]byte{}, fb...), fa...)
	if err := readBlocks(swapped, testPos, 32, 32); !errors.Is(err, erasure.ErrCorrupt) {
		t.Fatalf("swapped blocks: err = %v, want ErrCorrupt", err)
	}
}

// TestTransplantedBlocksAreDetected: a valid block taken from another
// position must fail at the position it is read from.
func TestTransplantedBlocksAreDetected(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, 24)
	other := []struct {
		name string
		pos  Position
	}{
		{"another shard index at the same offset", Position{Data: testPos.Data, Shard: testPos.Shard + 1}},
		{"another DataID at the same position", Position{Data: ids.DataID{0xEE}, Shard: testPos.Shard}},
		{"an older write of the same position for another DataID", Position{Data: ids.DataID{0x0D}, Shard: testPos.Shard}},
		{"the same shard at another block index", Position{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: 1}},
	}
	for _, tt := range other {
		t.Run(tt.name, func(t *testing.T) {
			// The copy is a block that verifies at its own position.
			copied := framedAt(t, tt.pos, payload)
			if err := readBlocks(copied, tt.pos, len(payload)); err != nil {
				t.Fatalf("copy does not verify at its own position: %v", err)
			}
			if err := readBlocks(copied, testPos, len(payload)); !errors.Is(err, erasure.ErrCorrupt) {
				t.Fatalf("transplanted block: err = %v, want ErrCorrupt", err)
			}
		})
	}
}

// TestWrongFirstBlockIsDetected: a range reader given the wrong first block
// index must reject the blocks it reads.
func TestWrongFirstBlockIsDetected(t *testing.T) {
	const first = 5
	blocks := [][]byte{[]byte("stripe five"), []byte("stripe six!")}
	stream := framedAt(t, Position{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: first}, blocks...)
	lens := []int{len(blocks[0]), len(blocks[1])}
	for _, got := range []uint64{first - 1, first + 1, 0} {
		p := Position{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: got}
		if err := readBlocks(stream, p, lens...); !errors.Is(err, erasure.ErrCorrupt) {
			t.Fatalf("first block %d: err = %v, want ErrCorrupt", got, err)
		}
	}
	if err := readBlocks(stream, Position{Data: testPos.Data, Shard: testPos.Shard, FirstBlock: first}, lens...); err != nil {
		t.Fatalf("correct first block: %v", err)
	}
}

func TestReaderTruncatedIsCorruptAndSticky(t *testing.T) {
	stream := framed(t, []byte("0123456789"))
	r := NewReader(bytes.NewReader(stream[:HashSize+4]), testPos)
	err := r.ReadBlock(make([]byte, 10))
	if !errors.Is(err, erasure.ErrCorrupt) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated block: err = %v", err)
	}
	if err2 := r.ReadBlock(make([]byte, 10)); !errors.Is(err2, erasure.ErrCorrupt) {
		t.Fatalf("second read after truncation: err = %v, want sticky error", err2)
	}
	if !errors.Is(r.Err(), erasure.ErrCorrupt) {
		t.Fatalf("Err() = %v", r.Err())
	}
}

func TestReaderIOErrorIsSticky(t *testing.T) {
	ioErr := errors.New("drive unplugged")
	stream := framed(t, []byte("0123456789"))
	src := &failingReader{data: stream[:5], err: ioErr}
	r := NewReader(src, testPos)
	err := r.ReadBlock(make([]byte, 10))
	if !errors.Is(err, ioErr) {
		t.Fatalf("I/O error: err = %v, want %v", err, ioErr)
	}
	if errors.Is(err, erasure.ErrCorrupt) {
		t.Fatal("I/O error must not be reported as corruption")
	}
	if err2 := r.ReadBlock(make([]byte, 10)); !errors.Is(err2, ioErr) {
		t.Fatalf("second read: err = %v, want sticky I/O error", err2)
	}
}

func TestDoneDetectsTrailingBytes(t *testing.T) {
	stream := framed(t, []byte("xy"))
	r := NewReader(bytes.NewReader(append(stream, 0)), testPos)
	if err := r.ReadBlock(make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); !errors.Is(err, erasure.ErrCorrupt) {
		t.Fatalf("Done with trailing byte: err = %v, want ErrCorrupt", err)
	}
}

func TestWriterPropagatesErrors(t *testing.T) {
	ioErr := errors.New("disk full")
	w := NewWriter(&failingWriter{limit: 4, err: ioErr}, testPos)
	if err := w.WriteBlock([]byte("0123456789")); !errors.Is(err, ioErr) {
		t.Fatalf("WriteBlock: err = %v, want %v", err, ioErr)
	}
	if err := w.WriteBlock([]byte("x")); !errors.Is(err, ioErr) {
		t.Fatalf("WriteBlock after failure: err = %v, want sticky error", err)
	}
}
