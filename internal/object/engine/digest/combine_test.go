package digest

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/eukalpia/unio/internal/object"
)

// crcOf returns the CRC-64/NVME of data as an integer.
func crcOf(t *testing.T, data []byte) uint64 {
	t.Helper()
	h, err := New(object.ChecksumCRC64NVME)
	if err != nil {
		t.Fatal(err)
	}
	h.Write(data)
	return binary.BigEndian.Uint64(h.Sum(nil))
}

func TestCombineCRC64NVMEMatchesDirectCRC(t *testing.T) {
	check := "123456789"
	for split := 0; split <= len(check); split++ {
		a, b := []byte(check[:split]), []byte(check[split:])
		got := CombineCRC64NVME(crcOf(t, a), crcOf(t, b), int64(len(b)))
		if want := crcOf(t, []byte(check)); got != want {
			t.Fatalf("split %d: combined %#x, direct %#x", split, got, want)
		}
	}
	if got := crcOf(t, []byte(check)); got != 0xae8b14860a799888 {
		t.Fatalf("check value %#x", got)
	}
}

func TestCombineCRC64NVMERandomSplits(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for round := 0; round < 200; round++ {
		data := make([]byte, rng.IntN(20000))
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		var parts [][]byte
		for rest := data; len(rest) > 0; {
			n := rng.IntN(len(rest)) + 1
			parts = append(parts, rest[:n])
			rest = rest[n:]
		}
		var acc uint64
		for i, p := range parts {
			c := crcOf(t, p)
			if i == 0 {
				acc = c
				continue
			}
			acc = CombineCRC64NVME(acc, c, int64(len(p)))
		}
		if want := crcOf(t, data); acc != want {
			t.Fatalf("round %d (%d bytes, %d parts): combined %#x, direct %#x", round, len(data), len(parts), acc, want)
		}
	}
}

func TestCombineCRC64NVMELongZeroRun(t *testing.T) {
	// A shift of many bytes exercises the high bits of the squaring chain.
	head := []byte("prefix")
	zeros := make([]byte, 1<<20+37)
	got := CombineCRC64NVME(crcOf(t, head), crcOf(t, zeros), int64(len(zeros)))
	if want := crcOf(t, append(bytes.Clone(head), zeros...)); got != want {
		t.Fatalf("combined %#x, direct %#x", got, want)
	}
}

func TestCombineCRC64NVMEEmptyPiece(t *testing.T) {
	c := crcOf(t, []byte("data"))
	if got := CombineCRC64NVME(c, crcOf(t, nil), 0); got != c {
		t.Fatalf("appending nothing changed the checksum: %#x, want %#x", got, c)
	}
}
