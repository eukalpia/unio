package remotedrive

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/eukalpia/unio/internal/cluster/clustertest"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// remoteFixture is a two-node cluster: node "a" serves drives, node "b"
// reaches them.
type remoteFixture struct {
	cluster *clustertest.Cluster
	server  *Server
}

// newRemoteFixture starts the cluster and a server on node "a".
func newRemoteFixture(t *testing.T, opts ServerOptions) *remoteFixture {
	t.Helper()
	c := clustertest.NewCluster(t, "a", "b")
	srv := NewServer(opts)
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Register(c.Nodes["a"]); err != nil {
		t.Fatal(err)
	}
	return &remoteFixture{cluster: c, server: srv}
}

// serve adds a fresh in-memory drive to the server and returns a client for
// it, made on node "b".
func (f *remoteFixture) serve(t *testing.T) (*clustertest.MemDrive, *Client) {
	t.Helper()
	backing := clustertest.NewMemDrive(ids.NewDriveID(), "a", 0)
	if err := f.server.Add(backing); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.server.Remove(backing.ID()) })
	return backing, NewClient(f.cluster.Nodes["b"], "a", backing.ID())
}

func TestRemoteDriveConformance(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{})
	runDriveSuite(t, func(t *testing.T) drive.Drive {
		_, client := f.serve(t)
		return client
	})
}

func TestMemDriveConformance(t *testing.T) {
	runDriveSuite(t, func(t *testing.T) drive.Drive {
		return clustertest.NewMemDrive(ids.NewDriveID(), "a", 0)
	})
}

func TestUnreachableNodeIsOffline(t *testing.T) {
	c := clustertest.NewCluster(t, "b")
	client := NewClient(c.Nodes["b"], "ghost", ids.NewDriveID())
	_, err := client.StatShard(context.Background(), ids.ShardID{})
	if !errors.Is(err, drive.ErrOffline) {
		t.Fatalf("error = %v, want drive.ErrOffline", err)
	}
	if s3err.CodeOf(err) != s3err.ServiceUnavailable {
		t.Fatalf("code = %s", s3err.CodeOf(err))
	}
}

func TestUnknownDriveIsOffline(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{})
	client := NewClient(f.cluster.Nodes["b"], "a", ids.NewDriveID())
	if _, err := client.Info(context.Background()); !errors.Is(err, drive.ErrOffline) {
		t.Fatalf("info of unknown drive: %v, want ErrOffline", err)
	}
}

func TestDriveFaultsKeepTheirSentinel(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{})
	backing, client := f.serve(t)
	ctx := context.Background()
	id := ids.ShardID{Data: ids.NewDataID()}
	if err := client.WriteShard(ctx, id, bytes.NewReader([]byte("x")), 1); err != nil {
		t.Fatal(err)
	}
	backing.SetFault(drive.ErrFaulty)
	if _, err := client.StatShard(ctx, id); !errors.Is(err, drive.ErrFaulty) {
		t.Fatalf("stat during fault: %v, want ErrFaulty", err)
	}
	backing.SetFault(nil)
	if _, err := client.StatShard(ctx, id); err != nil {
		t.Fatalf("stat after fault cleared: %v", err)
	}
}

func TestLargeShardRoundTrip(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{})
	_, client := f.serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data := make([]byte, 8<<20)
	_, _ = rand.Read(data)
	id := ids.ShardID{Data: ids.NewDataID(), Index: 3}
	if err := client.WriteShard(ctx, id, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatal(err)
	}
	rc, err := client.ReadShard(ctx, id, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("8 MiB shard corrupted in transit")
	}
}

func TestWalkPaginatesLargeDrives(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{MaxPage: 64})
	_, client := f.serve(t)
	ctx := context.Background()
	const shards = 300
	for i := range shards {
		id := ids.ShardID{Data: ids.NewDataID(), Index: uint16(i)}
		if err := client.WriteShard(ctx, id, bytes.NewReader([]byte{1}), 1); err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := client.WalkShards(ctx, func(drive.ShardInfo) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != shards {
		t.Fatalf("walk visited %d of %d shards", count, shards)
	}
}

func TestWalkCursorMustBeLive(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{})
	backing, _ := f.serve(t)
	node := f.cluster.Nodes["b"]
	ctx := context.Background()
	client := NewClient(node, "a", backing.ID())
	if err := client.WriteShard(ctx, ids.ShardID{Data: ids.NewDataID()}, bytes.NewReader([]byte{1}), 1); err != nil {
		t.Fatal(err)
	}
	var page uniov1.WalkShardsResponse
	if err := node.Call(ctx, "a", MethodWalkShards, walkReq(backing.ID(), []byte("no-such-cursor")), &page); err == nil {
		t.Fatal("unknown cursor accepted")
	} else if s3err.CodeOf(err) != s3err.InvalidArgument {
		t.Fatalf("unknown cursor error = %v", err)
	}
}

func TestWalkSessionLimitIsEnforced(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{MaxWalks: 1, MaxPage: 1})
	backing, client := f.serve(t)
	ctx := context.Background()
	for i := range 3 {
		id := ids.ShardID{Data: ids.NewDataID(), Index: uint16(i)}
		if err := client.WriteShard(ctx, id, bytes.NewReader([]byte{1}), 1); err != nil {
			t.Fatal(err)
		}
	}
	node := f.cluster.Nodes["b"]
	var first uniov1.WalkShardsResponse
	if err := node.Call(ctx, "a", MethodWalkShards, walkReq(backing.ID(), nil), &first); err != nil {
		t.Fatal(err)
	}
	if first.GetDone() {
		t.Fatal("walk finished in one page; test needs it to stay open")
	}
	var second uniov1.WalkShardsResponse
	err := node.Call(ctx, "a", MethodWalkShards, walkReq(backing.ID(), nil), &second)
	if s3err.CodeOf(err) != s3err.SlowDown {
		t.Fatalf("second concurrent walk error = %v, want SlowDown", err)
	}
}

func TestWalkSessionIdlesOut(t *testing.T) {
	f := newRemoteFixture(t, ServerOptions{WalkIdle: 50 * time.Millisecond, MaxPage: 1})
	backing, client := f.serve(t)
	ctx := context.Background()
	for i := range 2 {
		id := ids.ShardID{Data: ids.NewDataID(), Index: uint16(i)}
		if err := client.WriteShard(ctx, id, bytes.NewReader([]byte{1}), 1); err != nil {
			t.Fatal(err)
		}
	}
	node := f.cluster.Nodes["b"]
	var first uniov1.WalkShardsResponse
	if err := node.Call(ctx, "a", MethodWalkShards, walkReq(backing.ID(), nil), &first); err != nil {
		t.Fatal(err)
	}
	cursor := first.GetCursor()
	// Stay away from the session so that it idles out, then poll until the
	// server rejects the cursor. Each poll is a probe, not a renewal: a
	// rejected probe does not touch the session.
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("idle walk session was never expired")
		case <-time.After(100 * time.Millisecond):
		}
		var next uniov1.WalkShardsResponse
		err := node.Call(ctx, "a", MethodWalkShards, walkReq(backing.ID(), cursor), &next)
		if s3err.CodeOf(err) == s3err.InvalidArgument {
			return
		}
		t.Fatalf("expired session answered %v", err)
	}
}

// walkReq builds a one-shard walk request for drive id continuing cursor.
// An empty cursor starts a walk.
func walkReq(id ids.DriveID, cursor []byte) *uniov1.WalkShardsRequest {
	return &uniov1.WalkShardsRequest{Drive: id[:], Cursor: cursor, Limit: 1}
}

func TestExactReaderRules(t *testing.T) {
	cases := []struct {
		name    string
		src     []byte
		size    int64
		wantErr bool
	}{
		{name: "exact", src: []byte("abcdef"), size: 6},
		{name: "short", src: []byte("abc"), size: 6, wantErr: true},
		{name: "long", src: []byte("abcdefgh"), size: 6, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &exactReader{r: bytes.NewReader(tc.src), remaining: tc.size}
			got, err := io.ReadAll(r)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("read %q without error", got)
				}
				return
			}
			if err != nil || string(got) != string(tc.src) {
				t.Fatalf("read %q, %v", got, err)
			}
		})
	}
}
