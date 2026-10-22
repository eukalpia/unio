package keys

import (
	"bytes"
	"testing"
)

// FuzzParse feeds arbitrary bytes to every Parse function. It must never
// panic, and whenever a parse succeeds, re-encoding its result must give back
// the input exactly, because the encoding is canonical.
func FuzzParse(f *testing.F) {
	f.Add(ObjectVersion(1, []byte("a"), 5))
	f.Add(ObjectVersion(1, []byte{0x00, 0x01}, 0))
	f.Add(Upload(1, []byte("a"), []byte("xy")))
	f.Add(Part(1, []byte("xy"), 7))
	f.Add(Bucket([]byte("photos")))
	f.Add(KV([]byte("k")))
	f.Add(Control(1))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0xff})
	f.Fuzz(func(t *testing.T, data []byte) {
		if b, k, ts, err := ParseObjectVersion(data); err == nil {
			if got := ObjectVersion(b, k, ts); !bytes.Equal(got, data) {
				t.Errorf("ParseObjectVersion(%x) re-encodes to %x", data, got)
			}
		}
		if b, k, u, err := ParseUpload(data); err == nil {
			if got := Upload(b, k, u); !bytes.Equal(got, data) {
				t.Errorf("ParseUpload(%x) re-encodes to %x", data, got)
			}
		}
		if b, u, n, err := ParsePart(data); err == nil {
			if got := Part(b, u, n); !bytes.Equal(got, data) {
				t.Errorf("ParsePart(%x) re-encodes to %x", data, got)
			}
		}
		if name, err := ParseBucket(data); err == nil {
			if got := Bucket(name); !bytes.Equal(got, data) {
				t.Errorf("ParseBucket(%x) re-encodes to %x", data, got)
			}
		}
		if key, err := ParseKV(data); err == nil {
			if got := KV(key); !bytes.Equal(got, data) {
				t.Errorf("ParseKV(%x) re-encodes to %x", data, got)
			}
		}
		if b, err := ParseControl(data); err == nil {
			if got := Control(b); !bytes.Equal(got, data) {
				t.Errorf("ParseControl(%x) re-encodes to %x", data, got)
			}
		}
	})
}
