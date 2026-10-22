package admin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/object"
)

// clusterBody is the decoded body of GET cluster.
type clusterBody struct {
	ClusterID string `json:"clusterId"`
	Nodes     []struct {
		ID        string `json:"id"`
		Address   string `json:"address"`
		S3Address string `json:"s3Address"`
		Healthy   bool   `json:"healthy"`
	} `json:"nodes"`
	Sets []struct {
		ID           uint32 `json:"id"`
		DataShards   int    `json:"dataShards"`
		ParityShards int    `json:"parityShards"`
		Drives       []struct {
			ID     string `json:"id"`
			Node   string `json:"node"`
			Path   string `json:"path"`
			Online bool   `json:"online"`
			Total  uint64 `json:"total"`
			Free   uint64 `json:"free"`
			Used   uint64 `json:"used"`
		} `json:"drives"`
	} `json:"sets"`
	Capacity struct {
		Total uint64 `json:"total"`
		Used  uint64 `json:"used"`
		Free  uint64 `json:"free"`
	} `json:"capacity"`
}

func TestGetCluster(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	f.topo.SetDown("node-b", true)

	resp := f.serve(f.sigv4(credsOf(key, secret), http.MethodGet, BasePath+"/cluster", "", nil))
	expectStatus(t, resp, http.StatusOK)
	expectNoStore(t, resp)
	var out clusterBody
	mustUnmarshal(t, resp.Body, &out)

	if out.ClusterID != f.m.ClusterID {
		t.Errorf("clusterId = %q, want %q", out.ClusterID, f.m.ClusterID)
	}
	if len(out.Nodes) != 2 {
		t.Fatalf("nodes = %+v", out.Nodes)
	}
	a, b := out.Nodes[0], out.Nodes[1]
	if a.ID != "node-a" || a.Address != "10.0.0.1:9100" || a.S3Address != "10.0.0.1:9000" || !a.Healthy {
		t.Errorf("node-a = %+v", a)
	}
	if b.ID != "node-b" || b.Healthy {
		t.Errorf("node-b = %+v, want unhealthy", b)
	}

	if len(out.Sets) != 1 {
		t.Fatalf("sets = %+v", out.Sets)
	}
	set := out.Sets[0]
	if set.DataShards != 2 || set.ParityShards != 2 || len(set.Drives) != 4 {
		t.Fatalf("set = %+v, want 2 data, 2 parity and 4 drives", set)
	}
	// Drives are listed in shard order, which is the set's order in the map.
	byID := make(map[ids.DriveID]int, len(f.m.Drives))
	for i, loc := range f.m.Drives {
		byID[loc.ID] = i
	}
	for i, d := range set.Drives {
		want := f.m.Drives[byID[f.m.Sets[0].Drives[i]]]
		if d.ID != want.ID.String() || d.Node != string(want.Node) || d.Path != want.Path {
			t.Errorf("drive %d = %+v, want %s on %s at %s", i, d, want.ID, want.Node, want.Path)
		}
		if want.Node == "node-a" {
			if !d.Online || d.Total == 0 {
				t.Errorf("local drive %d = %+v, want online with capacity", i, d)
			}
		} else if d.Online || d.Total != 0 || d.Free != 0 || d.Used != 0 {
			t.Errorf("remote drive %d = %+v, want offline with no capacity", i, d)
		}
	}

	// The capacity is the sum over the drives that answer.
	var total, used uint64
	for _, d := range f.m.Drives {
		if d.Node != "node-a" {
			continue
		}
		info, err := f.topo.Drive(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		st, err := info.Info(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		total += st.Total
		used += st.Used
	}
	if out.Capacity.Total != total || out.Capacity.Used != used || out.Capacity.Free != total-used {
		t.Errorf("capacity = %+v, want total %d used %d", out.Capacity, total, used)
	}
}

// TestGetClusterDriveIDsAreHex checks the drive id form the TUI and console parse.
func TestGetClusterDriveIDsAreHex(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	resp := f.serve(f.sigv4(credsOf(key, secret), http.MethodGet, BasePath+"/cluster", "", nil))
	expectStatus(t, resp, http.StatusOK)
	var out clusterBody
	mustUnmarshal(t, resp.Body, &out)
	for _, d := range out.Sets[0].Drives {
		if len(d.ID) != 32 || strings.Trim(d.ID, "0123456789abcdef") != "" {
			t.Errorf("drive id %q is not 32 lower-case hex digits", d.ID)
		}
		if _, err := ids.ParseDriveID(d.ID); err != nil {
			t.Errorf("drive id %q does not parse: %v", d.ID, err)
		}
	}
}

func TestGetClusterErrors(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	creds := credsOf(key, secret)
	expectError(t, f.serve(f.sigv4(creds, http.MethodPost, BasePath+"/cluster", "", nil)), http.StatusMethodNotAllowed, "MethodNotAllowed")
	expectError(t, f.serve(f.sigv4(creds, http.MethodGet, BasePath+"/cluster/extra", "", nil)), http.StatusNotFound, "NoSuchEntity")
}

// TestGetClusterDriveTimeout checks that a drive that stalls is reported
// offline within the probe deadline, and does not hold up the other drives.
func TestGetClusterDriveTimeout(t *testing.T) {
	f := newFixture(t)
	key, secret := f.createUser("alice", fullPolicy)
	stalled := f.m.Drives[0].ID
	f.topo.SetDrive(stallingDrive{id: stalled})

	resp := f.serve(f.sigv4(credsOf(key, secret), http.MethodGet, BasePath+"/cluster", "", nil))
	expectStatus(t, resp, http.StatusOK)
	var out clusterBody
	mustUnmarshal(t, resp.Body, &out)
	for _, d := range out.Sets[0].Drives {
		if d.ID == stalled.String() && d.Online {
			t.Error("a stalled drive is reported online")
		}
	}
}

// stallingDrive is a drive whose Info waits for its context to end.
type stallingDrive struct {
	drive.Drive
	id ids.DriveID
}

// ID implements drive.Drive.
func (s stallingDrive) ID() ids.DriveID { return s.id }

// Info implements drive.Drive by waiting until the caller gives up.
func (s stallingDrive) Info(ctx context.Context) (drive.Info, error) {
	<-ctx.Done()
	return drive.Info{}, ctx.Err()
}

// errStore is an object layer whose bucket listing fails with an error that
// names internal detail.
type errStore struct {
	object.Layer
}

// ListBuckets implements object.Layer by failing with internal text.
func (errStore) ListBuckets(context.Context) ([]object.BucketInfo, error) {
	return nil, errors.New("storage: shard 7f3a at /var/lib/unio/secret-path is corrupt")
}

// TestInternalErrorsAreGeneric checks that a store failure reaches the client as
// a generic InternalError, without the internal text.
func TestInternalErrorsAreGeneric(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Layer = errStore{Layer: c.Layer} })
	key, secret := f.createUser("alice", fullPolicy)
	resp := f.serve(f.sigv4(credsOf(key, secret), http.MethodGet, BasePath+"/buckets", "", nil))
	expectError(t, resp, http.StatusInternalServerError, "InternalError")
	if strings.Contains(string(resp.Body), "secret-path") || strings.Contains(string(resp.Body), "shard") {
		t.Errorf("the response leaks internal detail: %s", resp.Body)
	}
}
