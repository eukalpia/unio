package keys

import (
	"bytes"
	"errors"
	"testing"
)

func TestAppendEscaped(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, ""},
		{"plain", []byte("a"), "61"},
		{"nul", []byte{0x00}, "00 ff"},
		{"soh", []byte{0x01}, "01"},
		{"nul then soh", []byte{0x00, 0x01}, "00 ff 01"},
		{"ff", []byte{0xff}, "ff"},
		{"two nul", []byte{0x00, 0x00}, "00 ff 00 ff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendEscaped(nil, tt.in)
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("AppendEscaped(%x) = %x, want %x", tt.in, got, want)
			}
		})
	}
}

func TestAppendEscapedAppendsToDst(t *testing.T) {
	got := AppendEscaped(mustHex("aa"), []byte{0x00, 0x61})
	if want := mustHex("aa 00 ff 61"); !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}

func TestAppendTerminated(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{"empty", nil, "00 01"},
		{"plain", []byte("a"), "61 00 01"},
		{"nul", []byte{0x00}, "00 ff 00 01"},
		{"soh", []byte{0x01}, "01 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendTerminated(nil, tt.in)
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("AppendTerminated(%x) = %x, want %x", tt.in, got, want)
			}
		})
	}
}

func TestReadTerminated(t *testing.T) {
	valid := []struct {
		name, in, comp, rest string
	}{
		{"empty component", "00 01", "", ""},
		{"plain with rest", "61 62 00 01 aa", "61 62", "aa"},
		{"escaped nul", "00 ff 01 00 01", "00 01", ""},
		{"rest starts with nul", "61 00 01 00 01", "61", "00 01"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			comp, rest, err := readTerminated("test", mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(comp, mustHex(tt.comp)) || !bytes.Equal(rest, mustHex(tt.rest)) {
				t.Errorf("got component %x and rest %x, want %s and %s", comp, rest, tt.comp, tt.rest)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"no terminator", "61 62"},
		{"dangling nul", "61 00"},
		{"invalid escape 02", "00 02 00 01"},
		{"invalid escape 00", "00 00 00 01"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := readTerminated("test", mustHex(tt.in))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
