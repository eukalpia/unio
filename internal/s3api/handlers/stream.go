package handlers

import (
	"errors"
	"io"
	"time"

	"github.com/cloudwego/hertz/pkg/network"

	"github.com/eukalpia/unio/internal/bufpool"
	"github.com/eukalpia/unio/internal/object"
)

// errStreamOwned is returned if anything writes to the response after the
// body has been handed to streamObject. The response is written only there.
var errStreamOwned = errors.New("handlers: response body is written by the streamer")

// errStreamCap is returned when a streamed response exceeds its overall cap.
var errStreamCap = errors.New("handlers: streamed response exceeded its time limit")

// streamFinalizer replaces Hertz's own response writer for a streamed
// response. Hertz calls Finalize after the handler, and by then the whole
// response has already been written, so Finalize does nothing.
type streamFinalizer struct{}

// Write implements network.ExtWriter; the streamer never writes through it.
func (streamFinalizer) Write([]byte) (int, error) { return 0, errStreamOwned }

// Flush implements network.ExtWriter.
func (streamFinalizer) Flush() error { return nil }

// Finalize implements network.ExtWriter.
func (streamFinalizer) Finalize() error { return nil }

// deadlineWriter writes to the connection and arms the write deadline before
// every write. The deadline is the idle timeout, shortened so that it never
// passes the overall cap of the response, if one is set. A client that keeps
// reading therefore keeps the response alive however long it takes, and a
// client that stops reading is disconnected after one idle period.
type deadlineWriter struct {
	conn network.Conn
	w    network.Writer
	idle time.Duration
	// cap is the instant after which the response must be complete. The zero
	// value means no cap.
	cap time.Time
}

// arm sets the write deadline for the next write.
func (d *deadlineWriter) arm() error {
	t := d.idle
	if !d.cap.IsZero() {
		remaining := time.Until(d.cap)
		if remaining <= 0 {
			return errStreamCap
		}
		if remaining < t {
			t = remaining
		}
	}
	if t <= 0 {
		return nil
	}
	return d.conn.SetWriteTimeout(t)
}

// Write implements io.Writer. Each call is flushed before it returns: the
// connection writer may keep a reference to large slices until it is flushed,
// and io.CopyBuffer reuses its buffer on the next read. It deliberately has no
// ReadFrom method, so that io.CopyBuffer uses the caller's buffer.
//
// A call reports bytes only once its flush has succeeded. A failed call
// reports none, so the sum of the counts is a lower bound on the bytes the
// client may have received, and the observer never counts bytes that were
// not sent.
func (d *deadlineWriter) Write(p []byte) (int, error) {
	if err := d.arm(); err != nil {
		return 0, err
	}
	n, err := d.w.WriteBinary(p)
	if err != nil {
		return 0, err
	}
	if err := d.w.Flush(); err != nil {
		return 0, err
	}
	return n, nil
}

// streamObject writes a GET response whose body is the object. The header
// block and the body go straight to the connection writer, and the body is
// copied with the buffer taken from the pool, so the response needs no
// intermediate copy of the object. streamObject owns buf and body: both are
// released on every path. A failure after the header has been written cannot
// be reported to the client as an S3 error, so the connection is closed and
// the failure is logged.
func (e *Env) streamObject(c *Call, body *object.Reader, buf *bufpool.Buffer) {
	defer buf.Release()
	defer func() { _ = body.Close() }()

	var cap time.Time
	if e.WriteCap > 0 {
		cap = time.Now().Add(e.WriteCap)
	}
	dw := &deadlineWriter{
		conn: c.RC.GetConn(),
		w:    c.RC.GetWriter(),
		idle: e.WriteIdle,
		cap:  cap,
	}
	// Hertz must not write its own response: the status line and headers are
	// written below, and the body follows them.
	c.RC.Response.HijackWriter(streamFinalizer{})

	if _, err := dw.Write(c.RC.Response.Header.AppendBytes(nil)); err != nil {
		e.abortStream(c, err)
		return
	}
	n, err := io.CopyBuffer(dw, struct{ io.Reader }{body.ReadCloser}, buf.Bytes())
	c.Stats.AddBytesStreamed(n)
	if err != nil {
		e.abortStream(c, err)
	}
}

// abortStream logs a failed streamed response and closes its connection, since
// the client has already received a partial response.
func (e *Env) abortStream(c *Call, err error) {
	e.Logger.Warn("streamed response aborted", "error", err.Error())
	_ = c.RC.GetConn().Close()
}
