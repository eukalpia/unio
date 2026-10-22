package bitrot

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/eukalpia/unio/internal/erasure"
)

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

func framed(t *testing.T, blocks ...[]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, b := range blocks {
		if err := w.WriteBlock(b); err != nil {
			t.Fatalf("WriteBlock: %v", err)
		}
	}
	return buf.Bytes()
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
			r := NewReader(bytes.NewReader(stream))
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
	sum := Sum(data)
	if !bytes.Equal(stream[:HashSize], sum[:]) || !bytes.Equal(stream[HashSize:], data) {
		t.Fatal("block is not [hash][data]")
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
			r := NewReader(bytes.NewReader(stream))

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

func TestReaderTruncatedIsCorruptAndSticky(t *testing.T) {
	stream := framed(t, []byte("0123456789"))
	r := NewReader(bytes.NewReader(stream[:HashSize+4]))
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
	r := NewReader(src)
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
	r := NewReader(bytes.NewReader(append(stream, 0)))
	if err := r.ReadBlock(make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	if err := r.Done(); !errors.Is(err, erasure.ErrCorrupt) {
		t.Fatalf("Done with trailing byte: err = %v, want ErrCorrupt", err)
	}
}

func TestWriterPropagatesErrors(t *testing.T) {
	ioErr := errors.New("disk full")
	w := NewWriter(&failingWriter{limit: 4, err: ioErr})
	if err := w.WriteBlock([]byte("0123456789")); !errors.Is(err, ioErr) {
		t.Fatalf("WriteBlock: err = %v, want %v", err, ioErr)
	}
	if err := w.WriteBlock([]byte("x")); !errors.Is(err, ioErr) {
		t.Fatalf("WriteBlock after failure: err = %v, want sticky error", err)
	}
}
