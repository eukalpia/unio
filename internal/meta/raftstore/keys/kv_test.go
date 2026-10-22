package keys

import (
	"bytes"
	"errors"
	"testing"
)

func TestKV(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"key", "6b", "4b 6b"},
		{"empty key", "", "4b"},
		{"raw nul and soh bytes", "00 01", "4b 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := KV(mustHex(tt.in)), mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("KV = %x, want %x", got, want)
			}
		})
	}
	if got, want := KVPrefix(), mustHex("4b"); !bytes.Equal(got, want) {
		t.Errorf("KVPrefix = %x, want %x", got, want)
	}
}

func TestParseKV(t *testing.T) {
	valid := []struct {
		name, in, want string
	}{
		{"key", "4b 6b", "6b"},
		{"empty key", "4b", ""},
		{"raw nul and soh bytes", "4b 00 01", "00 01"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			key, err := ParseKV(mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(key, mustHex(tt.want)) {
				t.Errorf("key = %x, want %s", key, tt.want)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"wrong leading byte", "42 6b"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseKV(mustHex(tt.in)); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
