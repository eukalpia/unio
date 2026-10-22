package cluster

import (
	"fmt"
	"testing"

	"github.com/eukalpia/unio/internal/ids"
)

func TestPartitionOf(t *testing.T) {
	counts := make([]int, 17)
	for i := range 16000 {
		p := PartitionOf(7, fmt.Sprintf("key-%d", i), 16)
		if p < 1 || p > 16 {
			t.Fatalf("partition %d out of range", p)
		}
		counts[p]++
	}
	for p := 1; p <= 16; p++ {
		if counts[p] < 700 || counts[p] > 1300 {
			t.Fatalf("partition %d has %d keys: poor distribution", p, counts[p])
		}
	}
	if a, b := PartitionOf(7, "a", 16), PartitionOf(7, "a", 16); a != b {
		t.Fatal("not deterministic")
	}
	if PartitionOf(1, "a", 0) != ids.SystemPartition {
		t.Fatal("zero partitions must map to the system group")
	}
}
