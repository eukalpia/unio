package reqinfo

import (
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
)

func TestSetGet(t *testing.T) {
	c := app.NewContext(0)
	if got := Get(c); got != (Info{}) {
		t.Fatalf("Get before Set = %+v, want zero", got)
	}
	want := Info{RequestID: "18A2B3C4D5E6F708", HostID: "node1"}
	Set(c, want)
	if got := Get(c); got != want {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
}

// TestGetForeignValue checks that a value of another type stored under the
// key, which no package can do but a future bug might, yields the zero Info
// instead of a panic in error rendering.
func TestGetForeignValue(t *testing.T) {
	c := app.NewContext(0)
	c.Set(key, "not an Info")
	if got := Get(c); got != (Info{}) {
		t.Fatalf("Get = %+v, want zero", got)
	}
}
