package payload

import (
	"bytes"
	"testing"
)

func TestNewBufferSizeBookkeeping(t *testing.T) {
	tests := []struct {
		name    string
		size    int
		wantErr bool
	}{
		{name: "zero bytes", size: 0},
		{name: "one byte", size: 1},
		{name: "4 KiB", size: 4 << 10},
		{name: "1 MiB", size: 1 << 20},
		{name: "negative size", size: -1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := NewBuffer(tc.size, 7)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewBuffer(%d) error = %v, wantErr %v", tc.size, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if b.Len() != tc.size || len(b.Bytes()) != tc.size {
				t.Fatalf("Len() = %d, len(Bytes()) = %d, want %d", b.Len(), len(b.Bytes()), tc.size)
			}
		})
	}
}

func TestNewBufferDeterministicPerSeed(t *testing.T) {
	a, err := NewBuffer(4096, 1)
	if err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	b, err := NewBuffer(4096, 1)
	if err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	c, err := NewBuffer(4096, 2)
	if err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("same seed produced different bytes")
	}
	if bytes.Equal(a.Bytes(), c.Bytes()) {
		t.Error("different seeds produced identical bytes")
	}
}

func TestNewBufferIsNotConstant(t *testing.T) {
	b, err := NewBuffer(4096, 99)
	if err != nil {
		t.Fatalf("NewBuffer: %v", err)
	}
	seen := make(map[byte]struct{})
	for _, v := range b.Bytes() {
		seen[v] = struct{}{}
	}
	if len(seen) < 128 {
		t.Fatalf("buffer has only %d distinct byte values, want random data", len(seen))
	}
}
