package keys

import (
	"bytes"
	"testing"
)

func TestMeta(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"counter name", "last-ts", "4d 6c 61 73 74 2d 74 73"},
		{"empty name", "", "4d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := Meta(tt.in), mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("Meta(%q) = %x, want %x", tt.in, got, want)
			}
		})
	}
}
