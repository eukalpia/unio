package remotedrive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
)

// walkPage is the number of shards a client asks for per walk page.
const walkPage = 256

// Transport is the part of a UniWire transport that a Client needs.
// *uniwire.Transport implements it.
type Transport interface {
	Call(ctx context.Context, to ids.NodeID, method string, req, resp cluster.Message) error
	Stream(ctx context.Context, to ids.NodeID, method string, payload []byte, body io.Reader, bodyLen int64) (uniwire.Reply, error)
}

// Client is a drive.Drive whose data lives on another node. Transport
// failures surface as drive.ErrOffline, and every operation honours its
// context. Client is safe for concurrent use.
type Client struct {
	tr     Transport
	node   ids.NodeID
	id     ids.DriveID
	closed atomic.Bool
}

// Client implements drive.Drive.
var _ drive.Drive = (*Client)(nil)

// NewClient returns a drive client for drive id hosted on node.
func NewClient(tr Transport, node ids.NodeID, id ids.DriveID) *Client {
	return &Client{tr: tr, node: node, id: id}
}

// ID implements drive.Drive.
func (c *Client) ID() ids.DriveID { return c.id }

// usable returns drive.ErrClosed once the client is closed.
func (c *Client) usable() error {
	if c.closed.Load() {
		return drive.ErrClosed
	}
	return nil
}

// mapErr turns a transport failure into drive.ErrOffline, keeping the cause.
// Errors reported by the drive itself pass through unchanged.
func (c *Client) mapErr(err error) error {
	if errors.Is(err, uniwire.ErrUnavailable) || errors.Is(err, uniwire.ErrClosed) {
		return fmt.Errorf("remotedrive: drive %s on %s: %w: %w", c.id, c.node, drive.ErrOffline, err)
	}
	return err
}

// WriteShard implements drive.Drive by streaming size bytes to the remote
// drive.
func (c *Client) WriteShard(ctx context.Context, id ids.ShardID, r io.Reader, size int64) error {
	if err := c.usable(); err != nil {
		return err
	}
	if size < 0 {
		return fmt.Errorf("remotedrive: negative shard size %d", size)
	}
	payload, err := (&uniov1.WriteShardHeader{Drive: c.id[:], Shard: shardRef(id)}).MarshalVT()
	if err != nil {
		return fmt.Errorf("remotedrive: encode write: %w", err)
	}
	var body io.Reader
	if size > 0 {
		body = &exactReader{r: r, remaining: size}
	}
	rep, err := c.tr.Stream(ctx, c.node, MethodWriteShard, payload, body, size)
	if err != nil {
		return c.mapErr(err)
	}
	if rep.Body != nil {
		_ = rep.Body.Close()
	}
	return nil
}

// errSourceLonger is returned when a shard source holds more bytes than the
// declared size.
var errSourceLonger = errors.New("remotedrive: shard source is longer than its declared size")

// exactReader yields exactly remaining bytes of r. The remote drive commits a
// shard as soon as it has the declared bytes, so the final chunk is released
// only after r has been checked to end there. A longer source therefore
// aborts the upload before the remote side can commit it, which keeps the
// drive contract that excess data stores nothing.
type exactReader struct {
	r         io.Reader
	remaining int64
}

// Read implements io.Reader.
func (e *exactReader) Read(p []byte) (int, error) {
	if e.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > e.remaining {
		p = p[:e.remaining]
	}
	n, err := e.r.Read(p)
	e.remaining -= int64(n)
	if errors.Is(err, io.EOF) && e.remaining > 0 {
		return n, io.ErrUnexpectedEOF
	}
	if err == nil && e.remaining == 0 {
		var probe [1]byte
		m, perr := e.r.Read(probe[:])
		switch {
		case m > 0:
			return 0, errSourceLonger
		case perr != nil && !errors.Is(perr, io.EOF):
			return 0, perr
		}
	}
	return n, err
}

// ReadShard implements drive.Drive. The returned reader pulls the body from
// the remote node; it must be closed.
func (c *Client) ReadShard(ctx context.Context, id ids.ShardID, offset, length int64) (io.ReadCloser, error) {
	if err := c.usable(); err != nil {
		return nil, err
	}
	payload, err := (&uniov1.ReadShardRequest{
		Drive:  c.id[:],
		Shard:  shardRef(id),
		Offset: offset,
		Length: length,
	}).MarshalVT()
	if err != nil {
		return nil, fmt.Errorf("remotedrive: encode read: %w", err)
	}
	rep, err := c.tr.Stream(ctx, c.node, MethodReadShard, payload, nil, 0)
	if err != nil {
		return nil, c.mapErr(err)
	}
	if rep.Body == nil {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}
	return rep.Body, nil
}

// StatShard implements drive.Drive.
func (c *Client) StatShard(ctx context.Context, id ids.ShardID) (drive.ShardInfo, error) {
	if err := c.usable(); err != nil {
		return drive.ShardInfo{}, err
	}
	var m uniov1.ShardMeta
	req := &uniov1.ShardRequest{Drive: c.id[:], Shard: shardRef(id)}
	if err := c.tr.Call(ctx, c.node, MethodStatShard, req, &m); err != nil {
		return drive.ShardInfo{}, c.mapErr(err)
	}
	return shardInfoOf(&m)
}

// DeleteShard implements drive.Drive.
func (c *Client) DeleteShard(ctx context.Context, id ids.ShardID) error {
	if err := c.usable(); err != nil {
		return err
	}
	var empty uniov1.Empty
	req := &uniov1.ShardRequest{Drive: c.id[:], Shard: shardRef(id)}
	return c.mapErr(c.tr.Call(ctx, c.node, MethodDeleteShard, req, &empty))
}

// WalkShards implements drive.Drive by fetching the walk one page at a time.
// fn runs on the caller's goroutine, and an error from it stops the walk.
func (c *Client) WalkShards(ctx context.Context, fn func(drive.ShardInfo) error) error {
	if err := c.usable(); err != nil {
		return err
	}
	var cursor []byte
	for {
		var page uniov1.WalkShardsResponse
		req := &uniov1.WalkShardsRequest{Drive: c.id[:], Cursor: cursor, Limit: walkPage}
		if err := c.tr.Call(ctx, c.node, MethodWalkShards, req, &page); err != nil {
			return c.mapErr(err)
		}
		for _, m := range page.GetShards() {
			info, err := shardInfoOf(m)
			if err != nil {
				return err
			}
			if err := fn(info); err != nil {
				return err
			}
		}
		if page.GetDone() {
			return nil
		}
		if len(page.GetCursor()) == 0 {
			return badRequest("walk reply without cursor")
		}
		cursor = page.GetCursor()
	}
}

// Info implements drive.Drive.
func (c *Client) Info(ctx context.Context) (drive.Info, error) {
	if err := c.usable(); err != nil {
		return drive.Info{}, err
	}
	var r uniov1.DriveInfo
	if err := c.tr.Call(ctx, c.node, MethodInfo, &uniov1.DriveRequest{Drive: c.id[:]}, &r); err != nil {
		return drive.Info{}, c.mapErr(err)
	}
	id, err := driveIDOf(r.GetId())
	if err != nil {
		return drive.Info{}, err
	}
	node := ids.NodeID(r.GetNode())
	if node == "" {
		node = c.node
	}
	return drive.Info{
		ID:       id,
		Node:     node,
		Endpoint: r.GetEndpoint(),
		Total:    r.GetTotal(),
		Free:     r.GetFree(),
		Used:     r.GetUsed(),
		Online:   r.GetOnline(),
	}, nil
}

// Close implements drive.Drive. It releases only the client: the remote drive
// keeps running. Later calls fail with drive.ErrClosed.
func (c *Client) Close() error {
	c.closed.Store(true)
	return nil
}
