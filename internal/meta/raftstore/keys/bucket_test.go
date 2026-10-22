package keys

import (
	"bytes"
	"errors"
	"testing"
)

func TestBucket(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"name", "70 68 6f 74 6f 73", "42 70 68 6f 74 6f 73"},
		{"empty name", "", "42"},
		{"raw nul and soh bytes", "00 01", "42 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := Bucket(mustHex(tt.in)), mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("Bucket = %x, want %x", got, want)
			}
		})
	}
	if got, want := BucketPrefix(), mustHex("42"); !bytes.Equal(got, want) {
		t.Errorf("BucketPrefix = %x, want %x", got, want)
	}
}

func TestParseBucket(t *testing.T) {
	valid := []struct {
		name, in, want string
	}{
		{"name", "42 70 68 6f 74 6f 73", "70 68 6f 74 6f 73"},
		{"empty name", "42", ""},
		{"raw nul and soh bytes", "42 00 01", "00 01"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			name, err := ParseBucket(mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Equal(name, mustHex(tt.want)) {
				t.Errorf("name = %x, want %s", name, tt.want)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"wrong leading byte", "4b 6b"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseBucket(mustHex(tt.in)); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
