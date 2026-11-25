package mapstore

import (
	"fmt"
	"testing"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/mappb"
	"github.com/eukalpia/unio/internal/ids"
)

// testDrive returns a fixed drive id whose first byte is n.
func testDrive(n int) ids.DriveID {
	var id ids.DriveID
	id[0] = byte(n)
	id[15] = 1
	return id
}

// testMap is a three-node cluster with two erasure sets of three drives
// (2+1) and four data partitions. Drive i sits on node i mod 3 at
// /data/d<i>.
func testMap() *cluster.Map {
	nodes := []cluster.NodeInfo{
		{ID: "n1", Addr: "n1:9100", S3Addr: "n1:9000"},
		{ID: "n2", Addr: "n2:9100", S3Addr: "n2:9000"},
		{ID: "n3", Addr: "n3:9100", S3Addr: "n3:9000"},
	}
	var drives []cluster.DriveLocation
	for i := 1; i <= 6; i++ {
		drives = append(drives, cluster.DriveLocation{
			ID:   testDrive(i),
			Node: nodes[(i-1)%3].ID,
			Path: fmt.Sprintf("/data/d%d", i),
		})
	}
	return &cluster.Map{
		Version:   1,
		ClusterID: "cluster-a",
		Nodes:     nodes,
		Drives:    drives,
		Sets: []cluster.SetInfo{
			{ID: 0, DataShards: 2, ParityShards: 1, Drives: []ids.DriveID{testDrive(1), testDrive(2), testDrive(3)}},
			{ID: 1, DataShards: 2, ParityShards: 1, Drives: []ids.DriveID{testDrive(4), testDrive(5), testDrive(6)}},
		},
		Partitions: []cluster.PartitionInfo{
			{ID: ids.SystemPartition, Replicas: []ids.NodeID{"n1", "n2", "n3"}},
			{ID: 1, Replicas: []ids.NodeID{"n1", "n2", "n3"}},
			{ID: 2, Replicas: []ids.NodeID{"n2", "n3", "n1"}},
			{ID: 3, Replicas: []ids.NodeID{"n3", "n1", "n2"}},
			{ID: 4, Replicas: []ids.NodeID{"n1", "n2", "n3"}},
		},
	}
}

// cloneMap returns a deep copy of m, made by a codec round trip that
// mappb tests show is exact.
func cloneMap(t *testing.T, m *cluster.Map) *cluster.Map {
	t.Helper()
	b, err := mappb.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	c, err := mappb.Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// encodedMap returns the bytes of m as Save stores them.
func encodedMap(t *testing.T, m *cluster.Map) []byte {
	t.Helper()
	b, err := encode(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
