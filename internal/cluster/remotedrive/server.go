package remotedrive

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/uniwire"
	"github.com/eukalpia/unio/internal/drive"
	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// Registrar is the part of a UniWire transport that a Server registers its
// handlers on. *uniwire.Transport implements it.
type Registrar interface {
	Register(method string, h cluster.Handler) error
	RegisterStream(method string, h uniwire.StreamHandler) error
}

// ServerOptions bounds the server's walk sessions.
type ServerOptions struct {
	// MaxWalks is the number of walk sessions that may run at once. Default 16.
	MaxWalks int
	// WalkIdle ends a walk session that has not been used for this long.
	// Default one minute.
	WalkIdle time.Duration
	// MaxPage is the largest page a request may ask for, and the buffer each
	// walk session fills ahead of its caller. Default 256.
	MaxPage int
}

// Server exposes local drives to peers over UniWire. Each drive is addressed by
// its id; a request for a drive this node does not host fails with
// drive.ErrOffline. Server is safe for concurrent use.
type Server struct {
	opts ServerOptions

	// mu guards the tables below and the timers of walk sessions.
	mu     sync.Mutex
	drives map[ids.DriveID]drive.Drive
	walks  map[string]*walk
	closed bool
}

// NewServer returns a server with no drives. Zero options take their defaults.
func NewServer(opts ServerOptions) *Server {
	if opts.MaxWalks <= 0 {
		opts.MaxWalks = 16
	}
	if opts.WalkIdle <= 0 {
		opts.WalkIdle = time.Minute
	}
	if opts.MaxPage <= 0 {
		opts.MaxPage = 256
	}
	return &Server{
		opts:   opts,
		drives: map[ids.DriveID]drive.Drive{},
		walks:  map[string]*walk{},
	}
}

// Add makes d available to peers. It fails if a drive with the same id is
// already served.
func (s *Server) Add(d drive.Drive) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.drives[d.ID()]; dup {
		return fmt.Errorf("remotedrive: drive %s already served", d.ID())
	}
	s.drives[d.ID()] = d
	return nil
}

// Remove stops serving the drive with id. It does not close the drive.
func (s *Server) Remove(id ids.DriveID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drives, id)
}

// Close ends every walk session. Walks in progress observe their context
// being cancelled. Close does not close the drives.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	walks := make([]*walk, 0, len(s.walks))
	for token, w := range s.walks {
		walks = append(walks, w)
		delete(s.walks, token)
		w.timer.Stop()
	}
	s.mu.Unlock()
	for _, w := range walks {
		w.cancel()
	}
	return nil
}

// Register installs the drive handlers on r.
func (s *Server) Register(r Registrar) error {
	rpcs := map[string]cluster.Handler{
		MethodStatShard:   s.statShard,
		MethodDeleteShard: s.deleteShard,
		MethodInfo:        s.info,
		MethodWalkShards:  s.walkShards,
	}
	for name, h := range rpcs {
		if err := r.Register(name, h); err != nil {
			return fmt.Errorf("remotedrive: register %s: %w", name, err)
		}
	}
	streams := map[string]uniwire.StreamHandler{
		MethodWriteShard: s.writeShard,
		MethodReadShard:  s.readShard,
	}
	for name, h := range streams {
		if err := r.RegisterStream(name, h); err != nil {
			return fmt.Errorf("remotedrive: register %s: %w", name, err)
		}
	}
	return nil
}

// lookup returns the served drive with id, or drive.ErrOffline.
func (s *Server) lookup(id ids.DriveID) (drive.Drive, error) {
	s.mu.Lock()
	d, ok := s.drives[id]
	s.mu.Unlock()
	if !ok {
		return nil, drive.ErrOffline
	}
	return d, nil
}

// lookupBytes is lookup for a raw drive id from a request.
func (s *Server) lookupBytes(b []byte) (drive.Drive, error) {
	id, err := driveIDOf(b)
	if err != nil {
		return nil, err
	}
	return s.lookup(id)
}

// statShard handles MethodStatShard.
func (s *Server) statShard(ctx context.Context, _ ids.NodeID, req []byte) ([]byte, error) {
	var r uniov1.ShardRequest
	if err := r.UnmarshalVT(req); err != nil {
		return nil, badRequest("stat")
	}
	d, err := s.lookupBytes(r.GetDrive())
	if err != nil {
		return nil, err
	}
	id, err := shardIDOf(r.GetShard())
	if err != nil {
		return nil, err
	}
	info, err := d.StatShard(ctx, id)
	if err != nil {
		return nil, err
	}
	return shardMeta(info).MarshalVT()
}

// deleteShard handles MethodDeleteShard.
func (s *Server) deleteShard(ctx context.Context, _ ids.NodeID, req []byte) ([]byte, error) {
	var r uniov1.ShardRequest
	if err := r.UnmarshalVT(req); err != nil {
		return nil, badRequest("delete")
	}
	d, err := s.lookupBytes(r.GetDrive())
	if err != nil {
		return nil, err
	}
	id, err := shardIDOf(r.GetShard())
	if err != nil {
		return nil, err
	}
	if err := d.DeleteShard(ctx, id); err != nil {
		return nil, err
	}
	return (&uniov1.Empty{}).MarshalVT()
}

// info handles MethodInfo.
func (s *Server) info(ctx context.Context, _ ids.NodeID, req []byte) ([]byte, error) {
	var r uniov1.DriveRequest
	if err := r.UnmarshalVT(req); err != nil {
		return nil, badRequest("info")
	}
	d, err := s.lookupBytes(r.GetDrive())
	if err != nil {
		return nil, err
	}
	in, err := d.Info(ctx)
	if err != nil {
		return nil, err
	}
	return (&uniov1.DriveInfo{
		Id:       in.ID[:],
		Node:     string(in.Node),
		Endpoint: in.Endpoint,
		Total:    in.Total,
		Free:     in.Free,
		Used:     in.Used,
		Online:   in.Online,
	}).MarshalVT()
}

// walkShards handles MethodWalkShards. An empty cursor starts a walk; any
// other cursor continues one.
func (s *Server) walkShards(ctx context.Context, _ ids.NodeID, req []byte) ([]byte, error) {
	var r uniov1.WalkShardsRequest
	if err := r.UnmarshalVT(req); err != nil {
		return nil, badRequest("walk")
	}
	id, err := driveIDOf(r.GetDrive())
	if err != nil {
		return nil, err
	}
	limit := int(r.GetLimit())
	if limit <= 0 || limit > s.opts.MaxPage {
		limit = s.opts.MaxPage
	}

	var w *walk
	if len(r.GetCursor()) == 0 {
		d, err := s.lookup(id)
		if err != nil {
			return nil, err
		}
		if w, err = s.startWalk(id, d); err != nil {
			return nil, err
		}
	} else {
		if w, err = s.lookupWalk(string(r.GetCursor()), id); err != nil {
			return nil, err
		}
	}

	page, err := s.nextPage(ctx, w, limit)
	if err != nil {
		return nil, err
	}
	out := &uniov1.WalkShardsResponse{Done: page.done, Cursor: nil}
	if !page.done {
		out.Cursor = []byte(page.cursor)
	}
	for _, info := range page.shards {
		out.Shards = append(out.Shards, shardMeta(info))
	}
	return out.MarshalVT()
}

// writeShard handles MethodWriteShard. The stream length is the shard size.
func (s *Server) writeShard(ctx context.Context, req *uniwire.StreamRequest) (*uniwire.StreamResponse, error) {
	var h uniov1.WriteShardHeader
	if err := h.UnmarshalVT(req.Payload); err != nil {
		return nil, badRequest("write")
	}
	d, err := s.lookupBytes(h.GetDrive())
	if err != nil {
		return nil, err
	}
	id, err := shardIDOf(h.GetShard())
	if err != nil {
		return nil, err
	}
	if err := d.WriteShard(ctx, id, req.Body, req.BodyLen); err != nil {
		return nil, err
	}
	return &uniwire.StreamResponse{}, nil
}

// readShard handles MethodReadShard. The reply body is the requested range,
// checked against the shard size before any read starts.
func (s *Server) readShard(ctx context.Context, req *uniwire.StreamRequest) (*uniwire.StreamResponse, error) {
	var r uniov1.ReadShardRequest
	if err := r.UnmarshalVT(req.Payload); err != nil {
		return nil, badRequest("read")
	}
	d, err := s.lookupBytes(r.GetDrive())
	if err != nil {
		return nil, err
	}
	id, err := shardIDOf(r.GetShard())
	if err != nil {
		return nil, err
	}
	offset := r.GetOffset()
	if offset < 0 {
		return nil, badRequest("read")
	}
	info, err := d.StatShard(ctx, id)
	if err != nil {
		return nil, err
	}
	if offset > info.Size {
		return nil, s3err.Errorf(s3err.InvalidRange, "offset %d beyond shard of %d bytes", offset, info.Size)
	}
	n := info.Size - offset
	if length := r.GetLength(); length >= 0 {
		if length > n {
			return nil, s3err.Errorf(s3err.InvalidRange, "range beyond shard of %d bytes", info.Size)
		}
		n = length
	}
	rc, err := d.ReadShard(ctx, id, offset, n)
	if err != nil {
		return nil, err
	}
	return &uniwire.StreamResponse{Body: rc, BodyLen: n}, nil
}
