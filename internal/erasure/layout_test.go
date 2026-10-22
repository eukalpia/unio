package erasure

import (
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestWriteQuorum(t *testing.T) {
	if (Layout{DataShards: 4, ParityShards: 2}).WriteQuorum() != 4 {
		t.Fatal("4+2 must need 4")
	}
	if (Layout{DataShards: 2, ParityShards: 2}).WriteQuorum() != 3 {
		t.Fatal("2+2 must need 3")
	}
}

func TestWritten(t *testing.T) {
	l := Layout{DataShards: 2, ParityShards: 1, Drives: []ids.DriveID{ids.NewDriveID(), {}, ids.NewDriveID()}}
	if l.Written() != 2 || l.TotalShards() != 3 {
		t.Fatalf("written %d total %d", l.Written(), l.TotalShards())
	}
}
