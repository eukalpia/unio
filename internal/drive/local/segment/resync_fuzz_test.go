package segment

import (
	"bytes"
	"testing"
)

// FuzzResync feeds arbitrary bytes to the resynchronising scanner. It must not
// panic. Every record it yields must be in bounds, carry a valid data checksum,
// and not overlap the others. Damaged ranges must be ordered, disjoint, lie
// inside the segment and contain no record. Together with the records they must
// account for every byte up to End.
func FuzzResync(f *testing.F) {
	valid, _ := layoutRecords([]byte("seed one"), bytes.Repeat([]byte{'x'}, 200), []byte("tail"))
	f.Add(valid)
	damaged := append([]byte(nil), valid...)
	damaged[HeaderSize+2] ^= 0x40
	f.Add(damaged)
	f.Add(valid[:len(valid)-7])
	f.Fuzz(func(t *testing.T, b []byte) {
		type span struct{ start, end int64 }
		var recs []span
		res, err := Resync(bytes.NewReader(b), int64(len(b)), func(r Record) error {
			size := RecordSize(int64(len(r.Data)))
			if r.Offset < 0 || r.Offset+size > int64(len(b)) {
				t.Fatalf("record at %d leaves the input", r.Offset)
			}
			if Checksum(r.Data) != r.Header.DataCRC {
				t.Fatalf("record at %d yielded with a bad data checksum", r.Offset)
			}
			recs = append(recs, span{r.Offset, r.Offset + size})
			return nil
		})
		if err != nil {
			t.Fatalf("resync of in-memory bytes failed: %v", err)
		}

		var covered int64
		prev := int64(0)
		for _, r := range recs {
			if r.start < prev {
				t.Fatalf("records overlap or are out of order at %d", r.start)
			}
			covered += r.end - r.start
			prev = r.end
		}
		if res.Records != len(recs) {
			t.Fatalf("result counts %d records, callback saw %d", res.Records, len(recs))
		}
		if res.End < 0 || res.End > int64(len(b)) || res.TailBytes != int64(len(b))-res.End {
			t.Fatalf("end %d and tail %d do not match input of %d bytes", res.End, res.TailBytes, len(b))
		}

		prevEnd := int64(0)
		for _, d := range res.Damaged {
			if d.Start < prevEnd || d.Start >= d.End || d.End > res.End {
				t.Fatalf("damaged range %v is out of order or outside [0, %d)", d, res.End)
			}
			for _, r := range recs {
				if r.start < d.End && d.Start < r.end {
					t.Fatalf("record at %d lies inside damaged range %v", r.start, d)
				}
			}
			covered += d.End - d.Start
			prevEnd = d.End
		}
		if covered != res.End {
			t.Fatalf("records and damage cover %d bytes of an end of %d", covered, res.End)
		}
	})
}
