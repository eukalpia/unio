package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/eukalpia/unio/internal/cluster/mapstore"
	"github.com/eukalpia/unio/internal/drive/local"
)

// objectSizes covers every storage tier: inline, a single erasure stripe,
// several stripes, and an empty object.
var objectSizes = map[string]int{
	"empty":       0,
	"inline":      100,
	"one-stripe":  300 << 10,
	"multistripe": 3<<20 + 17,
}

// randomBytes returns n random bytes.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// putAll stores one object per size in bucket and returns their contents.
func putAll(t *testing.T, c *s3.Client, bucket string) map[string][]byte {
	t.Helper()
	ctx := context.Background()
	if _, err := c.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	want := make(map[string][]byte)
	for key, size := range objectSizes {
		data := randomBytes(t, size)
		if _, err := c.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(key), Body: bytes.NewReader(data),
		}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
		want[key] = data
	}
	return want
}

// checkAll reads every object back and compares it byte for byte.
func checkAll(t *testing.T, c *s3.Client, bucket string, want map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	for key, data := range want {
		out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
		if err != nil {
			t.Fatalf("get %s: %v", key, err)
		}
		got, err := io.ReadAll(out.Body)
		_ = out.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", key, err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("object %s: got %d bytes that differ from the %d stored", key, len(got), len(data))
		}
	}
	list, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list.Contents) != len(want) {
		t.Fatalf("list returned %d objects, want %d", len(list.Contents), len(want))
	}
}

// TestSingleNodeServesAndPersists runs a one-node, four-drive cluster through
// the S3 API: objects of every tier and a multipart upload are stored and read
// back, and they are still there after the node restarts on the same drives.
func TestSingleNodeServesAndPersists(t *testing.T) {
	n := newTestNode(t, "127.0.0.1", 4)
	cfg := loadConfig(t, n, []*testNode{n}, nil)

	r := serve(t, cfg)
	waitReady(t, r.node.S3Addr().String())
	c := client(r.node.S3Addr().String())
	want := putAll(t, c, "photos")
	want["multipart"] = multipartUpload(t, c, "photos", "multipart")
	checkAll(t, c, "photos", want)
	r.stop(t)

	r = serve(t, loadConfig(t, n, []*testNode{n}, nil))
	waitReady(t, r.node.S3Addr().String())
	checkAll(t, client(r.node.S3Addr().String()), "photos", want)
	r.stop(t)
}

// multipartUpload stores a two-part object and returns its content.
func multipartUpload(t *testing.T, c *s3.Client, bucket, key string) []byte {
	t.Helper()
	ctx := context.Background()
	up, err := c.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		t.Fatalf("create upload: %v", err)
	}
	parts := [][]byte{randomBytes(t, 5<<20), randomBytes(t, 1<<20+3)}
	var done []types.CompletedPart
	for i, p := range parts {
		num := aws.Int32(int32(i + 1))
		out, err := c.UploadPart(ctx, &s3.UploadPartInput{
			Bucket: aws.String(bucket), Key: aws.String(key), UploadId: up.UploadId,
			PartNumber: num, Body: bytes.NewReader(p),
		})
		if err != nil {
			t.Fatalf("upload part %d: %v", i+1, err)
		}
		done = append(done, types.CompletedPart{ETag: out.ETag, PartNumber: num})
	}
	if _, err := c.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: aws.String(bucket), Key: aws.String(key), UploadId: up.UploadId,
		MultipartUpload: &types.CompletedMultipartUpload{Parts: done},
	}); err != nil {
		t.Fatalf("complete upload: %v", err)
	}
	return append(parts[0], parts[1]...)
}

// TestRestartRefusesChangedLayout checks that a node whose configuration no
// longer matches the cluster it formed refuses to start, instead of
// bootstrapping a different cluster over the existing data.
func TestRestartRefusesChangedLayout(t *testing.T) {
	n := newTestNode(t, "127.0.0.1", 4)
	r := serve(t, loadConfig(t, n, []*testNode{n}, nil))
	waitReady(t, r.node.S3Addr().String())
	r.stop(t)

	cfg := loadConfig(t, n, []*testNode{n}, map[string]string{"UNIO_METADATA_PARTITIONS": "8"})
	node, err := Start(context.Background(), cfg, discardLogger())
	if err == nil {
		_ = node.Close()
		t.Fatal("node started with a changed partition count")
	}
	if !errors.Is(err, mapstore.ErrMismatch) {
		t.Fatalf("start error = %v, want mapstore.ErrMismatch", err)
	}
}

// TestRefusesDriveFromAnotherSlot swaps two formatted drives. Each now holds
// the identity of the other's slot, and the node must refuse to serve them.
func TestRefusesDriveFromAnotherSlot(t *testing.T) {
	n := newTestNode(t, "127.0.0.1", 4)
	r := serve(t, loadConfig(t, n, []*testNode{n}, nil))
	waitReady(t, r.node.S3Addr().String())
	r.stop(t)

	tmp := n.dirs[0] + ".swap"
	for _, mv := range [][2]string{{n.dirs[0], tmp}, {n.dirs[1], n.dirs[0]}, {tmp, n.dirs[1]}} {
		if err := os.Rename(mv[0], mv[1]); err != nil {
			t.Fatal(err)
		}
	}
	node, err := Start(context.Background(), loadConfig(t, n, []*testNode{n}, nil), discardLogger())
	if err == nil {
		_ = node.Close()
		t.Fatal("node started with two drives swapped between slots")
	}
	if !errors.Is(err, local.ErrDriveIDMismatch) {
		t.Fatalf("start error = %v, want local.ErrDriveIDMismatch", err)
	}
}

// TestFailedStartReleasesResources makes the last step of the start fail,
// by occupying the S3 port, and checks that every earlier step was undone:
// the same node then starts normally on the same drives and ports.
func TestFailedStartReleasesResources(t *testing.T) {
	n := newTestNode(t, "127.0.0.1", 4)
	cfg := loadConfig(t, n, []*testNode{n}, nil)

	blocker, err := listenOn(cfg.Node.S3Address)
	if err != nil {
		t.Fatal(err)
	}
	if node, err := Start(context.Background(), cfg, discardLogger()); err == nil {
		_ = node.Close()
		t.Fatal("start succeeded with the S3 port taken")
	}
	_ = blocker.Close()

	r := serve(t, loadConfig(t, n, []*testNode{n}, nil))
	waitReady(t, r.node.S3Addr().String())
	r.stop(t)
}

// TestThreeNodeCluster forms a cluster of three nodes on separate loopback
// addresses. An object written through one node is read through the others,
// which exercises remote drives, metadata forwarding and the Raft groups
// across real mutual-TLS connections.
func TestThreeNodeCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("cluster test in -short mode")
	}
	nodes := []*testNode{
		newTestNode(t, "127.0.0.1", 4),
		newTestNode(t, "127.0.0.2", 4),
		newTestNode(t, "127.0.0.3", 4),
	}
	var rs []*running
	for _, n := range nodes {
		rs = append(rs, serve(t, loadConfig(t, n, nodes, nil)))
	}
	for _, r := range rs {
		waitReady(t, r.node.S3Addr().String())
	}
	want := putAll(t, client(rs[0].node.S3Addr().String()), "shared")
	for _, r := range rs[1:] {
		checkAll(t, client(r.node.S3Addr().String()), "shared", want)
	}
	for _, r := range rs {
		r.stop(t)
	}
}
