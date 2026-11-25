package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"testing/iotest"
)

// collect returns a sink that copies every chunk into dst at its offset.
func collect(dst []byte) func(p []byte, off int64) error {
	return func(p []byte, off int64) error {
		copy(dst[off:], p)
		return nil
	}
}

func overlapBufs(n int) [2][]byte { return [2][]byte{make([]byte, n), make([]byte, n)} }

func TestCopyOverlappedDeliversEveryByteInPlace(t *testing.T) {
	for _, size := range []int64{1, 7, 8, 9, 16, 17, 1000} {
		src := bytes.Repeat([]byte("0123456789abcdef"), 64)[:size]
		dst := make([]byte, size)
		// OneByteReader makes every ReadFull take many calls, as a pipe does.
		if err := copyOverlapped(context.Background(), overlapBufs(8), iotest.OneByteReader(bytes.NewReader(src)), size, collect(dst)); err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(dst, src) {
			t.Fatalf("size %d: bytes differ", size)
		}
	}
}

func TestCopyOverlappedRejectsShortAndLongReaders(t *testing.T) {
	src := bytes.Repeat([]byte{1}, 40)
	if err := copyOverlapped(context.Background(), overlapBufs(8), bytes.NewReader(src[:30]), 40, collect(make([]byte, 40))); err == nil {
		t.Fatal("short reader accepted")
	}
	if err := copyOverlapped(context.Background(), overlapBufs(8), bytes.NewReader(src), 30, collect(make([]byte, 40))); err == nil {
		t.Fatal("long reader accepted")
	}
	boom := errors.New("boom")
	r := io.MultiReader(bytes.NewReader(src[:20]), iotest.ErrReader(boom))
	if err := copyOverlapped(context.Background(), overlapBufs(8), r, 40, collect(make([]byte, 40))); !errors.Is(err, boom) {
		t.Fatalf("reader error lost: %v", err)
	}
}

func TestCopyOverlappedStopsAtSinkError(t *testing.T) {
	boom := errors.New("disk full")
	calls := 0
	sink := func(p []byte, off int64) error {
		calls++
		if off >= 16 {
			return boom
		}
		return nil
	}
	src := bytes.NewReader(make([]byte, 8000))
	err := copyOverlapped(context.Background(), overlapBufs(8), src, 8000, sink)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want the sink error", err)
	}
	// The reader runs at most a couple of chunks ahead of the failing sink.
	if calls > 3 || src.Len() < 8000-8*6 {
		t.Fatalf("copy kept going after the sink failed: %d sink calls, %d bytes left", calls, src.Len())
	}
}

func TestCopyOverlappedHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sink := func(p []byte, off int64) error {
		cancel()
		return nil
	}
	err := copyOverlapped(ctx, overlapBufs(8), bytes.NewReader(make([]byte, 800)), 800, sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
