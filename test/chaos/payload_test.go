package chaos

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestNewPayloadProperties(t *testing.T) {
	tests := []struct {
		name string
		seed uint64
		seq  uint64
	}{
		{"zero", 0, 0},
		{"small", 1, 2},
		{"large", 1 << 60, 1<<40 + 7},
		{"max seq", 99, ^uint64(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPayload(tt.seed, tt.seq)
			if n := len(p.Body); n < minPayloadSize || n > maxPayloadSize {
				t.Fatalf("size %d outside [%d, %d]", n, minPayloadSize, maxPayloadSize)
			}
			if p.Digest != sha256.Sum256(p.Body) {
				t.Fatal("digest does not match body")
			}
			again := NewPayload(tt.seed, tt.seq)
			if !bytes.Equal(p.Body, again.Body) || p.Digest != again.Digest {
				t.Fatal("payload is not deterministic")
			}
		})
	}
}

func TestNewPayloadVariesWithInputs(t *testing.T) {
	base := NewPayload(7, 100)
	tests := []struct {
		name string
		p    Payload
	}{
		{"different seq", NewPayload(7, 101)},
		{"different seed", NewPayload(8, 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.p.Digest == base.Digest {
				t.Fatal("distinct inputs produced the same digest")
			}
		})
	}
}
