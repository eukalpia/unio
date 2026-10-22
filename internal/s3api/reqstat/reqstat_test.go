package reqstat

import (
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestAcquireStartsCleared(t *testing.T) {
	st := Acquire()
	if st.Op != UnknownOp || st.BytesIn != 0 || st.BytesStreamed != 0 {
		t.Fatalf("Acquire = %+v, want the unknown op and zero counts", *st)
	}
	st.Op, st.BytesIn, st.BytesStreamed = "GetObject", 5, 7
	Release(st)
	// A pooled record may come back; whichever comes back must be cleared.
	if again := Acquire(); again.Op != UnknownOp || again.BytesIn != 0 || again.BytesStreamed != 0 {
		t.Fatalf("reused record = %+v, want it cleared", *again)
	}
}

func TestSetGet(t *testing.T) {
	c := app.NewContext(0)
	if Get(c) != nil {
		t.Fatal("Get on a request without a record is not nil")
	}
	st := Acquire()
	Set(c, st)
	if Get(c) != st {
		t.Fatal("Get did not return the record set")
	}
	c.Set(key, "not a record")
	if Get(c) != nil {
		t.Fatal("Get of a foreign value is not nil")
	}
}

// TestNilStatsIgnoresUpdates checks that handlers may update a record that is
// absent, as they do when no observer is configured.
func TestNilStatsIgnoresUpdates(t *testing.T) {
	var st *Stats
	st.SetOp("PutObject")
	st.AddBytesIn(10)
	st.AddBytesStreamed(10)
}

func TestCountsIgnoreNonPositive(t *testing.T) {
	st := Acquire()
	defer Release(st)
	st.AddBytesIn(-1)
	st.AddBytesStreamed(-1)
	st.AddBytesIn(3)
	st.AddBytesStreamed(4)
	if st.BytesIn != 3 || st.BytesStreamed != 4 {
		t.Fatalf("counts = %d, %d; want 3, 4", st.BytesIn, st.BytesStreamed)
	}
}
