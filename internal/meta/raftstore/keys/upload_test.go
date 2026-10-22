package keys

import (
	"bytes"
	"errors"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestUpload(t *testing.T) {
	tests := []struct {
		name     string
		bucket   ids.BucketID
		key      string
		uploadID string
		want     string
	}{
		{"plain", 1, "61", "78 79", "55 " + bucket1 + " 61 00 01 78 79"},
		{"empty key and upload id", 1, "", "", "55 " + bucket1 + " 00 01"},
		{"nul key and nul upload id", 1, "00", "00 01", "55 " + bucket1 + " 00 ff 00 01 00 01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Upload(tt.bucket, mustHex(tt.key), mustHex(tt.uploadID))
			if want := mustHex(tt.want); !bytes.Equal(got, want) {
				t.Errorf("Upload = %x, want %x", got, want)
			}
		})
	}
}

func TestUploadPrefixes(t *testing.T) {
	if got, want := UploadBucketPrefix(1), mustHex("55 "+bucket1); !bytes.Equal(got, want) {
		t.Errorf("UploadBucketPrefix = %x, want %x", got, want)
	}
	if got, want := UploadKeyPrefix(1, []byte("a")), mustHex("55 "+bucket1+" 61 00 01"); !bytes.Equal(got, want) {
		t.Errorf("UploadKeyPrefix = %x, want %x", got, want)
	}
	upload := Upload(1, []byte("a"), []byte("xy"))
	if !bytes.HasPrefix(upload, UploadKeyPrefix(1, []byte("a"))) {
		t.Errorf("upload %x lacks its key prefix", upload)
	}
	if !bytes.HasPrefix(upload, UploadBucketPrefix(1)) {
		t.Errorf("upload %x lacks its bucket prefix", upload)
	}
}

func TestParseUpload(t *testing.T) {
	valid := []struct {
		name, in, key, uploadID string
		bucket                  ids.BucketID
	}{
		{"plain", "55 " + bucket1 + " 61 00 01 78 79", "61", "78 79", 1},
		{"empty key and upload id", "55 " + bucket1 + " 00 01", "", "", 1},
		{"nul key and nul upload id", "55 " + bucket1 + " 00 ff 00 01 00 01", "00", "00 01", 1},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			b, key, uploadID, err := ParseUpload(mustHex(tt.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if b != tt.bucket || !bytes.Equal(key, mustHex(tt.key)) || !bytes.Equal(uploadID, mustHex(tt.uploadID)) {
				t.Errorf("got (%d, %x, %x), want (%d, %s, %s)", b, key, uploadID, tt.bucket, tt.key, tt.uploadID)
			}
		})
	}

	malformed := []struct {
		name, in string
	}{
		{"empty input", ""},
		{"wrong leading byte", "4f " + bucket1 + " 61 00 01 78 79"},
		{"short bucket id", "55 00 00"},
		{"unterminated key", "55 " + bucket1 + " 61 62"},
		{"dangling nul", "55 " + bucket1 + " 61 00"},
		{"invalid escape", "55 " + bucket1 + " 00 02 00 01 78"},
	}
	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := ParseUpload(mustHex(tt.in))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("err = %v, want ErrMalformed", err)
			}
		})
	}
}
