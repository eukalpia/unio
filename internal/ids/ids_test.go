package ids

import "testing"

func TestShardIDBinary(t *testing.T) {
	s := ShardID{Data: NewDataID(), Index: 513}
	b := s.AppendBinary(nil)
	if len(b) != ShardIDSize {
		t.Fatalf("len %d", len(b))
	}
	got, err := ParseShardIDBinary(b)
	if err != nil || got != s {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := ParseShardIDBinary(b[:5]); err == nil {
		t.Fatal("expected error for short input")
	}
}

func TestHexRoundTrip(t *testing.T) {
	d := NewDataID()
	if got, err := ParseDataID(d.String()); err != nil || got != d {
		t.Fatalf("data id: %v %v", got, err)
	}
	r := NewDriveID()
	if got, err := ParseDriveID(r.String()); err != nil || got != r {
		t.Fatalf("drive id: %v %v", got, err)
	}
	for _, bad := range []string{"", "abc", "zz" + d.String()[2:]} {
		if _, err := ParseDataID(bad); err == nil {
			t.Fatalf("%q: expected error", bad)
		}
	}
}

func TestZero(t *testing.T) {
	if !(DataID{}).IsZero() || NewDataID().IsZero() || !(DriveID{}).IsZero() {
		t.Fatal("IsZero")
	}
}
