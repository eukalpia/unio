package segment

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

// layoutRecords encodes payloads as consecutive records. It returns the bytes
// and the offset at which each record starts. Record i has shard index i+1.
func layoutRecords(payloads ...[]byte) ([]byte, []int64) {
	var buf []byte
	var offs []int64
	for i, p := range payloads {
		offs = append(offs, int64(len(buf)))
		buf = AppendRecord(buf, 0, testID(uint16(i+1)), p)
	}
	return buf, offs
}

// resyncAll runs Resync over b and returns the data of each yielded record,
// keyed by shard index, together with the result.
func resyncAll(t *testing.T, b []byte) (map[uint16][]byte, ResyncResult) {
	t.Helper()
	got := map[uint16][]byte{}
	res, err := Resync(bytes.NewReader(b), int64(len(b)), func(r Record) error {
		got[r.Header.ShardID.Index] = append([]byte(nil), r.Data...)
		return nil
	})
	if err != nil {
		t.Fatalf("resync: %v", err)
	}
	return got, res
}

func TestResyncRecoversRecordsAroundDamage(t *testing.T) {
	payloads := [][]byte{
		[]byte("alpha"),
		bytes.Repeat([]byte{'b'}, 300),
		bytes.Repeat([]byte{'c'}, 40),
		bytes.Repeat([]byte{'d'}, 500),
		[]byte("omega"),
	}
	b, offs := layoutRecords(payloads...)
	b[offs[1]] ^= 0xff              // record 2: header magic destroyed
	b[offs[2]+HeaderSize+5] ^= 0xff // record 3: one data byte flipped

	got, res := resyncAll(t, b)
	if len(got) != 3 {
		t.Fatalf("recovered %d records, want 3", len(got))
	}
	for _, keep := range []uint16{1, 4, 5} {
		if !bytes.Equal(got[keep], payloads[keep-1]) {
			t.Fatalf("intact record %d was not recovered intact", keep)
		}
	}
	if want := []Range{{offs[1], offs[3]}}; !reflect.DeepEqual(res.Damaged, want) {
		t.Fatalf("damaged ranges %v, want %v", res.Damaged, want)
	}
	if res.Records != 3 || res.End != int64(len(b)) || res.TailBytes != 0 {
		t.Fatalf("result %+v: want 3 records, end %d, no tail", res, len(b))
	}
}

// TestResyncNeverAcceptsForgedHeaderInPayload places record-shaped bytes inside
// the payload of a damaged record. Resynchronisation must skip them unless their
// own checksums verify: one has a bad header checksum, the other a bad data
// checksum.
func TestResyncNeverAcceptsForgedHeaderInPayload(t *testing.T) {
	badHeader := AppendRecord(nil, 0, testID(90), []byte("bad header"))
	badHeader[HeaderSize-1] ^= 0xff // header checksum no longer matches
	badData := AppendRecord(nil, 0, testID(91), []byte("bad data"))
	badData[HeaderSize] ^= 0xff // data checksum no longer matches

	payload := append([]byte("prefix"), badHeader...)
	payload = append(payload, badData...)
	payload = append(payload, []byte("suffix")...)
	b, offs := layoutRecords([]byte("alpha"), payload, []byte("omega"))
	b[offs[1]] ^= 0xff // the record carrying the forged bytes is damaged

	got, _ := resyncAll(t, b)
	for _, forged := range []uint16{90, 91} {
		if _, ok := got[forged]; ok {
			t.Fatalf("forged record %d was accepted", forged)
		}
	}
	if len(got) != 2 || !bytes.Equal(got[1], []byte("alpha")) || !bytes.Equal(got[3], []byte("omega")) {
		t.Fatalf("recovered shards %d, want alpha and omega only", len(got))
	}
}

func TestResyncReportsDamageAndTornTail(t *testing.T) {
	b, offs := layoutRecords([]byte("one"), []byte("two"), []byte("three"))
	b[offs[1]] ^= 0xff
	torn := AppendRecord(nil, 0, testID(7), bytes.Repeat([]byte{'t'}, 100))
	b = append(b, torn[:HeaderSize+30]...)

	got, res := resyncAll(t, b)
	if len(got) != 2 || res.Records != 2 {
		t.Fatalf("recovered %d records, want 2", len(got))
	}
	if want := []Range{{offs[1], offs[2]}}; !reflect.DeepEqual(res.Damaged, want) {
		t.Fatalf("damaged ranges %v, want %v", res.Damaged, want)
	}
	end := offs[2] + RecordSize(int64(len("three")))
	if res.End != end || res.TailBytes != int64(len(b))-end {
		t.Fatalf("end %d tail %d; want end %d tail %d", res.End, res.TailBytes, end, int64(len(b))-end)
	}
}

func TestResyncEmptyAndAllZero(t *testing.T) {
	for _, b := range [][]byte{nil, make([]byte, 4096)} {
		got, res := resyncAll(t, b)
		if len(got) != 0 || res.Records != 0 || res.End != 0 || len(res.Damaged) != 0 {
			t.Fatalf("input of %d bytes: %+v with %d shards", len(b), res, len(got))
		}
		if res.TailBytes != int64(len(b)) {
			t.Fatalf("tail %d, want %d", res.TailBytes, len(b))
		}
	}
}

func TestResyncPropagatesCallbackError(t *testing.T) {
	b, _ := layoutRecords([]byte("x"), []byte("y"))
	stop := errors.New("stop")
	_, err := Resync(bytes.NewReader(b), int64(len(b)), func(Record) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the callback error", err)
	}
}

// TestResyncRejectsHeaderLongerThanTheSegment places a header whose checksum is
// valid but whose length exceeds the segment. It must not be yielded, and the
// scan must not allocate for the claimed length.
func TestResyncRejectsHeaderLongerThanTheSegment(t *testing.T) {
	forged := Header{ShardID: testID(5), Length: 1 << 29, DataCRC: 0}.AppendHeader(nil)
	forged = append(forged, []byte("short")...)
	got, res := resyncAll(t, forged)
	if len(got) != 0 || res.Records != 0 || res.TailBytes != int64(len(forged)) {
		t.Fatalf("forged header accepted: %+v with %d shards", res, len(got))
	}
}
