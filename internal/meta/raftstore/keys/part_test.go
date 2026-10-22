package keys

import (
	"bytes"
	"errors"
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestPart(t *testing.T) {
	tests := []struct {
		name     string
		uploadID string
		number   uint32
		want     string
	}{
		{"plain", "78 79", 7, "50 " + bucket1 + " 78 79 00 01 00 00 00 07"},
		{"empty upload id", "", 1, "50 " + bucket1 + " 00 01 00 00 00 01"},
		{"nul upload id", "00", math.MaxUint32, "50 " + bucket1 + " 00 ff 00 01 ff ff ff ff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Part(1, mustHex(tt.uploadID), tt.number)
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("Part = %x, want %x", got, want)
			}
		})
	}
}

func TestPartUploadPrefix(t *testing.T) {
	got := PartUploadPrefix(1, []byte("xy"))
	if want := mustHex("50 " + bucket1 + " 78 79 00 01"); !bytes.Equal(got, want) {
		t.Errorf("PartUploadPrefix = %x, want %x", got, want)
	}
	if !bytes.HasPrefix(Part(1, []byte("xy"), 3), got) {
		t.Errorf("part key lacks the upload prefix")
	}
}

func TestParsePart(t *testing.T) {
	valid := []struct {
		name, in, uploadID string
		bucket             ids.BucketID
		number             uint32
	}{
		{"plain", "50 " + bucket1 + " 78 79 00 01 00 00 00 07", "78 79", 1, 7},
		{"empty upload id", "50 " + bucket1 + " 00 01 00 00 00 01", "", 1, 1},
		{"max part number", "50 " + bucket1 + " 00 ff 00 01 ff ff ff ff", "00", 1, math.MaxUint32},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			b, uploadID, n, err := ParsePart(mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b != tt.bucket || !bytes.Equal(uploadID, mustHex(tt.uploadID)) || n != tt.number {
				t.Errorf("got (%d, %x, %d), want (%d, %s, %d)", b, uploadID, n, tt.bucket, tt.uploadID, tt.number)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"wrong leading byte", "55 " + bucket1 + " 78 79 00 01 00 00 00 07"},
		{"short bucket id", "50 00 00"},
		{"unterminated upload id", "50 " + bucket1 + " 78 79"},
		{"invalid escape", "50 " + bucket1 + " 00 00 00 01 00 00 00 07"},
		{"missing part number", "50 " + bucket1 + " 78 79 00 01"},
		{"short part number", "50 " + bucket1 + " 78 79 00 01 00 00 07"},
		{"long part number", "50 " + bucket1 + " 78 79 00 01 00 00 00 07 00"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := ParsePart(mustHex(tt.in))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
