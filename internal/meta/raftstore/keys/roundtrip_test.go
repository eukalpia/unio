package keys

import (
	"bytes"
	"math"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

// FuzzRecordRoundTrip checks that the Parse function of each record kind
// inverts its constructor for arbitrary bytes, including NUL and SOH.
func FuzzRecordRoundTrip(f *testing.F) {
	f.Add(uint64(1), []byte("a"), []byte("xy"), uint32(7), []byte("photos"))
	f.Add(uint64(0), []byte{0x00, 0x01}, []byte{0x00, 0x01}, uint32(math.MaxUint32), []byte{0x00})
	f.Add(uint64(math.MaxUint64), []byte{}, []byte{}, uint32(0), []byte{})
	f.Fuzz(func(t *testing.T, bucket uint64, key, uploadID []byte, number uint32, name []byte) {
		b := ids.BucketID(bucket)

		gb, gk, gu, err := ParseUpload(Upload(b, key, uploadID))
		if err != nil || gb != b || !bytes.Equal(gk, key) || !bytes.Equal(gu, uploadID) {
			t.Fatalf("upload round trip: got (%d, %x, %x, %v)", gb, gk, gu, err)
		}

		pb, pu, pn, err := ParsePart(Part(b, uploadID, number))
		if err != nil || pb != b || !bytes.Equal(pu, uploadID) || pn != number {
			t.Fatalf("part round trip: got (%d, %x, %d, %v)", pb, pu, pn, err)
		}

		gname, err := ParseBucket(Bucket(name))
		if err != nil || !bytes.Equal(gname, name) {
			t.Fatalf("bucket round trip: got (%x, %v)", gname, err)
		}

		gkv, err := ParseKV(KV(key))
		if err != nil || !bytes.Equal(gkv, key) {
			t.Fatalf("kv round trip: got (%x, %v)", gkv, err)
		}

		cb, err := ParseControl(Control(b))
		if err != nil || cb != b {
			t.Fatalf("control round trip: got (%d, %v)", cb, err)
		}
	})
}
