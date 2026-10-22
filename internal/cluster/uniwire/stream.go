package uniwire

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"runtime/debug"
	"sync"
	"time"

	"github.com/eukalpia/unio/internal/ids"
	uniov1 "github.com/eukalpia/unio/internal/pb/unio/v1"
	"github.com/eukalpia/unio/internal/s3err"
)

// A stream exchange moves one blob of known length over a connection that
// carries nothing else. The caller sends:
//
//	StreamHeader {method, payload, length, timeout}  (length bytes follow)
//
// The callee answers with:
//
//	StreamHeader {payload, length}  (length bytes follow)
//	raw bytes
//	StreamTrailer {error}           (status of the exchange)
//
// Bytes are never buffered or framed: io.CopyBuffer moves them between the
// connection and the caller's reader using pooled buffers. A connection whose
// body cannot be completed is closed, because its framing is then lost.

// errStaleConn reports that a reused idle connection was already closed by
// the peer before the request was sent. Nothing reached the peer, so the call
// may be retried on a fresh connection.
var errStaleConn = errors.New("uniwire: stale stream connection")

// errBodyClosed is the error a reply body reports after being closed early.
var errBodyClosed = errors.New("uniwire: reply body closed before the end")

// StreamRequest is a stream request as seen by its handler.
type StreamRequest struct {
	// From is the authenticated caller.
	From ids.NodeID
	// Payload is the method-specific request.
	Payload []byte
	// Body yields exactly BodyLen bytes from the caller. Whatever the handler
	// leaves unread is drained before the reply.
	Body io.Reader
	// BodyLen is the number of body bytes, zero when the caller sent none.
	BodyLen int64
}

// StreamResponse is a stream handler's successful result.
type StreamResponse struct {
	// Payload is the method-specific response.
	Payload []byte
	// Body, when set, yields exactly BodyLen bytes. It is closed after the
	// reply has been written.
	Body    io.ReadCloser
	BodyLen int64
}

// StreamHandler serves one stream method. A handler that returns an error
// must not return a body; any body it returns is closed.
type StreamHandler func(ctx context.Context, req *StreamRequest) (*StreamResponse, error)

// Reply is the callee's answer to a stream call.
type Reply struct {
	// Payload is the method-specific response.
	Payload []byte
	// Body yields BodyLen bytes of reply data. It is nil when the reply has
	// no body. A non-nil Body must be read to the end or closed.
	Body    io.ReadCloser
	BodyLen int64
}

// Stream performs one stream call to node to. It sends payload and, when
// bodyLen is positive, exactly bodyLen bytes read from body. It then waits for
// the callee's reply header. A reply with a body is returned as Reply.Body,
// and the connection stays checked out until that body is read or closed.
//
// Failures on the path to the peer wrap ErrUnavailable. Errors from body
// itself are returned as they are, because they are local.
func (t *Transport) Stream(ctx context.Context, to ids.NodeID, method string, payload []byte, body io.Reader, bodyLen int64) (Reply, error) {
	if err := checkMethod(method); err != nil {
		return Reply{}, err
	}
	if bodyLen < 0 || (bodyLen > 0 && body == nil) {
		return Reply{}, errors.New("uniwire: stream body length and reader disagree")
	}
	if t.isClosed() {
		return Reply{}, ErrClosed
	}
	if remainingNanos(ctx) < 0 {
		return Reply{}, ctx.Err()
	}
	p := t.peerFor(to)
	fresh := false
	for {
		sc, err := t.acquireStream(ctx, p, fresh)
		if err != nil {
			return Reply{}, err
		}
		rep, err := t.exchange(ctx, p, sc, method, payload, body, bodyLen)
		if errors.Is(err, errStaleConn) && !fresh {
			fresh = true
			continue
		}
		return rep, err
	}
}

// exchange runs one stream call on sc. It owns sc and returns it to the pool
// or closes it before returning, except for a reply body, which returns it
// when read or closed.
func (t *Transport) exchange(ctx context.Context, p *peer, sc *streamConn, method string, payload []byte, body io.Reader, bodyLen int64) (Reply, error) {
	sc.nextID++
	id := sc.nextID
	stop := context.AfterFunc(ctx, func() { _ = sc.nc.Close() })
	if err := sc.nc.SetDeadline(transferDeadline(ctx, t.lim.StreamTimeout)); err != nil {
		stop()
		t.finishStream(p, sc, false)
		return Reply{}, unavailable(ctx, p.id, err)
	}

	head, err := appendMessageFrame(nil, t.lim.MaxFrameSize, &uniov1.StreamHeader{
		Id:           id,
		Method:       method,
		Payload:      payload,
		Length:       bodyLen,
		TimeoutNanos: remainingNanos(ctx),
	})
	if err != nil {
		// Nothing was written, so the connection is still healthy.
		stop()
		t.finishStream(p, sc, true)
		return Reply{}, err
	}
	if _, err := sc.nc.Write(head); err != nil {
		stop()
		t.finishStream(p, sc, false)
		if sc.reused && ctx.Err() == nil {
			return Reply{}, errStaleConn
		}
		return Reply{}, unavailable(ctx, p.id, err)
	}
	if bodyLen > 0 {
		fromSource, err := t.copyBody(ctx, sc.nc, body, bodyLen)
		if err != nil {
			stop()
			t.finishStream(p, sc, false)
			if fromSource {
				return Reply{}, err
			}
			return Reply{}, unavailable(ctx, p.id, err)
		}
	}

	var rh uniov1.StreamHeader
	if err := readMessageFrame(sc.nc, t.lim.MaxFrameSize, &rh); err != nil {
		stop()
		t.finishStream(p, sc, false)
		return Reply{}, unavailable(ctx, p.id, err)
	}
	if rh.Id != id || rh.Length < 0 {
		stop()
		t.finishStream(p, sc, false)
		return Reply{}, unavailable(ctx, p.id, errMalformedFrame)
	}
	if rh.Length > 0 {
		return Reply{
			Payload: rh.Payload,
			Body: &streamBody{
				t: t, p: p, sc: sc, ctx: ctx, stop: stop, id: id, remaining: rh.Length,
			},
			BodyLen: rh.Length,
		}, nil
	}

	status, err := readTrailer(sc.nc, t.lim.MaxFrameSize, id)
	stop()
	if err != nil {
		t.finishStream(p, sc, false)
		return Reply{}, unavailable(ctx, p.id, err)
	}
	t.finishStream(p, sc, true)
	if status != nil {
		return Reply{}, decodeError(status)
	}
	return Reply{Payload: rh.Payload}, nil
}

// copyBody writes exactly n bytes from body to nc. fromSource reports whether
// a read from body, rather than a write to nc, caused the failure.
func (t *Transport) copyBody(ctx context.Context, nc net.Conn, body io.Reader, n int64) (fromSource bool, err error) {
	buf, err := t.clientBuffers.Get(ctx)
	if err != nil {
		return false, err
	}
	defer buf.Release()
	src := &sourceReader{r: io.LimitReader(body, n)}
	written, err := io.CopyBuffer(nc, src, buf.Bytes())
	switch {
	case src.err != nil:
		return true, src.err
	case err != nil:
		return false, err
	case written != n:
		return true, io.ErrUnexpectedEOF
	}
	return false, nil
}

// sourceReader records a non-EOF error from the caller's reader, so that
// copyBody can tell a failing source from a failing connection.
type sourceReader struct {
	r   io.Reader
	err error
}

// Read implements io.Reader.
func (s *sourceReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
	return n, err
}

// readTrailer reads the trailer that ends a reply and returns its error,
// nil on success.
func readTrailer(nc net.Conn, max int, id uint64) (*uniov1.RPCError, error) {
	var tr uniov1.StreamTrailer
	if err := readMessageFrame(nc, max, &tr); err != nil {
		return nil, err
	}
	if tr.Id != id {
		return nil, fmt.Errorf("%w: trailer id %d, want %d", errMalformedFrame, tr.Id, id)
	}
	return tr.Error, nil
}

// transferDeadline returns the earlier of the context deadline and now plus
// limit.
func transferDeadline(ctx context.Context, limit time.Duration) time.Time {
	d := time.Now().Add(limit)
	if cd, ok := ctx.Deadline(); ok && cd.Before(d) {
		d = cd
	}
	return d
}

// streamBody is the reply body of a stream call. It reads exactly BodyLen
// bytes, then the trailer, and returns the connection to the pool once both
// are consumed.
type streamBody struct {
	t         *Transport
	p         *peer
	sc        *streamConn
	ctx       context.Context
	stop      func() bool
	id        uint64
	remaining int64
	released  bool
	// err is sticky: io.EOF after a complete body, otherwise the failure.
	err error
}

// Read implements io.Reader. It is not safe to call Read and Close
// concurrently, which io.ReadCloser users do not do.
func (b *streamBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.sc.nc.Read(p)
	b.remaining -= int64(n)
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		b.abort(err)
		return n, b.err
	}
	if b.remaining == 0 {
		return n, b.finish()
	}
	return n, nil
}

// finish reads the trailer after a complete body and releases the
// connection. It returns nil when the exchange succeeded.
func (b *streamBody) finish() error {
	status, err := readTrailer(b.sc.nc, b.t.lim.MaxFrameSize, b.id)
	if err != nil {
		b.abort(err)
		return b.err
	}
	b.release(true)
	if status != nil {
		b.err = decodeError(status)
		return b.err
	}
	b.err = io.EOF
	return nil
}

// abort closes the connection because the body cannot be completed, and
// records the failure.
func (b *streamBody) abort(cause error) {
	b.release(false)
	b.err = unavailable(b.ctx, b.p.id, cause)
}

// release returns the connection to the pool, or closes it when it is not
// healthy. It runs once.
func (b *streamBody) release(healthy bool) {
	if b.released {
		return
	}
	b.released = true
	b.stop()
	b.t.finishStream(b.p, b.sc, healthy)
}

// Close releases the connection. A body that was not read to the end leaves
// the connection in an unknown state, so it is closed rather than reused.
func (b *streamBody) Close() error {
	if !b.released {
		b.abort(errBodyClosed)
	}
	if b.err == nil {
		b.err = errBodyClosed
	}
	return nil
}

// serveStream serves one inbound stream connection, one transfer at a time,
// until the connection fails or the peer goes idle.
func (t *Transport) serveStream(nc net.Conn, from ids.NodeID) {
	for {
		if err := nc.SetReadDeadline(time.Now().Add(t.lim.IdleTimeout)); err != nil {
			return
		}
		var hdr uniov1.StreamHeader
		if err := readMessageFrame(nc, t.lim.MaxFrameSize, &hdr); err != nil {
			return
		}
		if !t.serveTransfer(nc, from, &hdr) {
			return
		}
	}
}

// serveTransfer runs the handler for one stream request and writes its reply.
// It reports whether the connection can carry another transfer.
func (t *Transport) serveTransfer(nc net.Conn, from ids.NodeID, hdr *uniov1.StreamHeader) bool {
	if hdr.Length < 0 || hdr.TimeoutNanos < 0 {
		return false
	}
	limit := t.lim.StreamTimeout
	if hdr.TimeoutNanos > 0 {
		limit = min(limit, time.Duration(hdr.TimeoutNanos))
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	if err := nc.SetDeadline(time.Now().Add(limit)); err != nil {
		return false
	}

	body := &bodyReader{r: nc, remaining: hdr.Length}
	h := t.streamHandler(hdr.Method)
	if h == nil {
		return t.drain(body) && t.writeFailure(nc, hdr.Id, ErrUnknownMethod)
	}
	resp, err := t.invokeStream(ctx, h, hdr.Method, &StreamRequest{
		From:    from,
		Payload: hdr.Payload,
		Body:    body,
		BodyLen: hdr.Length,
	})
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return t.drain(body) && t.writeFailure(nc, hdr.Id, err)
	}
	if resp == nil {
		resp = &StreamResponse{}
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.BodyLen < 0 || (resp.Body == nil && resp.BodyLen > 0) {
		return false
	}
	if !t.drain(body) {
		return false
	}
	return t.writeSuccess(ctx, nc, hdr.Id, resp)
}

// writeSuccess writes a successful reply: the header, the body and a
// trailer that reports success. A failure while writing the body leaves the
// caller unable to frame the reply, so the connection must close.
func (t *Transport) writeSuccess(ctx context.Context, nc net.Conn, id uint64, resp *StreamResponse) bool {
	head, err := appendMessageFrame(nil, t.lim.MaxFrameSize, &uniov1.StreamHeader{
		Id:      id,
		Payload: resp.Payload,
		Length:  resp.BodyLen,
	})
	if err != nil {
		return false
	}
	if _, err := nc.Write(head); err != nil {
		return false
	}
	if resp.BodyLen > 0 {
		buf, err := t.serveBuffers.Get(ctx)
		if err != nil {
			return false
		}
		written, err := io.CopyBuffer(nc, io.LimitReader(resp.Body, resp.BodyLen), buf.Bytes())
		buf.Release()
		if err != nil || written != resp.BodyLen {
			return false
		}
	}
	return t.writeTrailer(nc, id, nil)
}

// writeFailure writes a reply that carries no body and reports failure.
func (t *Transport) writeFailure(nc net.Conn, id uint64, failure error) bool {
	head, err := appendMessageFrame(nil, t.lim.MaxFrameSize, &uniov1.StreamHeader{Id: id})
	if err != nil {
		return false
	}
	if _, err := nc.Write(head); err != nil {
		return false
	}
	return t.writeTrailer(nc, id, failure)
}

// writeTrailer writes the trailer that ends a reply. A nil status reports
// success.
func (t *Transport) writeTrailer(nc net.Conn, id uint64, status error) bool {
	tr := &uniov1.StreamTrailer{Id: id}
	if status != nil {
		tr.Error = encodeError(status)
	}
	b, err := appendMessageFrame(nil, t.lim.MaxFrameSize, tr)
	if err != nil {
		return false
	}
	_, err = nc.Write(b)
	return err == nil
}

// drainBufferSize is the size of the scratch buffer that drain discards an
// unread request body through.
const drainBufferSize = 32 << 10

// drainBuffers holds the scratch buffers of drain, so that a drain does not
// allocate. The buffers come from no budget.
var drainBuffers = sync.Pool{
	New: func() any {
		b := make([]byte, drainBufferSize)
		return &b
	},
}

// drain reads and discards whatever part of body the handler left unread, so
// that the caller can finish sending before it reads the reply. It takes no
// buffer from a budget, because a drain on the inbound side must never wait
// for memory that outbound transfers hold. The connection deadline set by
// serveTransfer bounds how long it runs.
func (t *Transport) drain(body *bodyReader) bool {
	if body.remaining == 0 {
		return true
	}
	bp := drainBuffers.Get().(*[]byte)
	defer drainBuffers.Put(bp)
	for body.remaining > 0 {
		if _, err := body.Read(*bp); err != nil && !errors.Is(err, io.EOF) {
			return false
		}
	}
	return true
}

// invokeStream runs a stream handler, recovering from panics the same way as
// invoke does for RPC handlers.
func (t *Transport) invokeStream(ctx context.Context, h StreamHandler, method string, req *StreamRequest) (resp *StreamResponse, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.log.Error("uniwire: stream handler panicked", "method", method, "from", req.From, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			resp, err = nil, s3err.Wrap(s3err.InternalError, fmt.Errorf("handler panic: %v", r))
		}
	}()
	return h(ctx, req)
}

// bodyReader reads exactly remaining bytes from the connection. It reports
// io.ErrUnexpectedEOF if the connection ends early.
type bodyReader struct {
	r         io.Reader
	remaining int64
}

// Read implements io.Reader.
func (b *bodyReader) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	if errors.Is(err, io.EOF) && b.remaining > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}
