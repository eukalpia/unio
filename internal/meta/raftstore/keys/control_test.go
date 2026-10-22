package keys

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestControl(t *testing.T) {
	if got, want := Control(1), mustHex("46 "+bucket1); !bytes.Equal(got, want) {
		t.Errorf("Control(1) = %x, want %x", got, want)
	}
}

func TestParseControl(t *testing.T) {
	valid := []struct {
		name, in string
		bucket   ids.BucketID
	}{
		{"bucket 1", "46 " + bucket1, 1},
		{"max bucket", "46 ff ff ff ff ff ff ff ff", ids.BucketID(^uint64(0))},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			b, err := ParseControl(mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b != tt.bucket {
				t.Errorf("bucket = %d, want %d", b, tt.bucket)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"wrong leading byte", "4f " + bucket1},
		{"short bucket id", "46 00 00 00"},
		{"trailing byte", "46 " + bucket1 + " 00"},
		{"prefix only", "46"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseControl(mustHex(tt.in)); !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
