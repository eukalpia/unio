package topology

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"runtime"
	"testing"

	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/ids"
)

// goldenSecret is 32 bytes, the shortest secret pki accepts. The vectors below
// pin the derivations: changing one orphans every formatted drive and splits
// clusters, so a change must be a new format version, never an edit.
var goldenSecret = []byte("0123456789abcdef0123456789abcdef")

func TestDeriveClusterIDGolden(t *testing.T) {
	const want = "c4fb2363d6664f17f72bda05aa17a6f3"
	got, err := DeriveClusterID(goldenSecret)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("DeriveClusterID = %s, want %s", got, want)
	}
}

func TestDeriveClusterIDShape(t *testing.T) {
	got, err := DeriveClusterID(goldenSecret)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 32 {
		t.Fatalf("cluster id %q has %d characters, want 32", got, len(got))
	}
	for _, c := range got {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("cluster id %q is not lowercase hex", got)
		}
	}
}

func TestDeriveClusterIDRejectsShortSecret(t *testing.T) {
	cases := []struct {
		name   string
		secret []byte
		err    bool
	}{
		{name: "nil", secret: nil, err: true},
		{name: "one byte short", secret: goldenSecret[:pki.MinSecretLen-1], err: true},
		{name: "minimum", secret: goldenSecret[:pki.MinSecretLen], err: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DeriveClusterID(tc.secret)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
		})
	}
}

func TestDeriveClusterIDFollowsTheSecret(t *testing.T) {
	a, err := DeriveClusterID(goldenSecret)
	if err != nil {
		t.Fatal(err)
	}
	again, err := DeriveClusterID(append([]byte(nil), goldenSecret...))
	if err != nil {
		t.Fatal(err)
	}
	if a != again {
		t.Fatal("same secret gave different cluster ids")
	}
	other := append([]byte(nil), goldenSecret...)
	other[len(other)-1] ^= 1
	b, err := DeriveClusterID(other)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("secrets that differ in one bit gave the same cluster id")
	}
}

// goldenPath is a drive path that filepath.Clean leaves unchanged on Linux,
// where the drive vectors were computed.
const goldenPath = "/data1"

func TestDriveIDForGolden(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the drive vectors are computed from Linux paths")
	}
	const want = "d877820e9937bb8ee69df745df7d392c"
	got := DriveIDFor("0123456789abcdef0123456789abcdef", "node-1", goldenPath)
	if got.String() != want {
		t.Fatalf("DriveIDFor = %s, want %s", got, want)
	}
}

func TestDriveIDForCleansThePath(t *testing.T) {
	const cluster = "c1"
	const node ids.NodeID = "n1"
	base := DriveIDFor(cluster, node, "/data1")
	for _, p := range []string{"/data1/", "/data1/.", "/data1//", "/data1/./"} {
		if got := DriveIDFor(cluster, node, p); got != base {
			t.Fatalf("path %q gives %s, want the id of /data1: %s", p, got, base)
		}
	}
}

func TestDriveIDForDistinctInputs(t *testing.T) {
	base := DriveIDFor("c1", "n1", "/data1")
	cases := []struct {
		name string
		id   ids.DriveID
	}{
		{name: "other cluster", id: DriveIDFor("c2", "n1", "/data1")},
		{name: "other node", id: DriveIDFor("c1", "n2", "/data1")},
		{name: "other path", id: DriveIDFor("c1", "n1", "/data2")},
		{name: "longer path with the same prefix", id: DriveIDFor("c1", "n1", "/data10")},
		{name: "cluster and node run together", id: DriveIDFor("c1n", "1", "/data1")},
		{name: "node and path run together", id: DriveIDFor("c1", "n1/", "data1")},
	}
	seen := map[ids.DriveID]string{base: "base"}
	for _, tc := range cases {
		if tc.id == base {
			t.Fatalf("%s gives the same id as the base drive", tc.name)
		}
		if prev, dup := seen[tc.id]; dup {
			t.Fatalf("%s and %s give the same id", tc.name, prev)
		}
		seen[tc.id] = tc.name
	}
}

func TestDriveIDForIsNeverZero(t *testing.T) {
	// The zero digest cannot come from SHA-256 of any input in practice, so
	// the fallback is exercised directly.
	var zero [sha256.Size]byte
	got := driveIDFromDigest(zero)
	if got.IsZero() {
		t.Fatal("zero digest gave the zero drive id")
	}
	want := sha256.Sum256(zero[:])
	if !bytes.Equal(got[:], want[:16]) {
		t.Fatalf("zero digest fallback = %s, want the first 16 bytes of its hash", got)
	}
	for i := range 1000 {
		if DriveIDFor("c1", "n1", fmt.Sprintf("/d%d", i)).IsZero() {
			t.Fatalf("drive %d has the zero id", i)
		}
	}
}
