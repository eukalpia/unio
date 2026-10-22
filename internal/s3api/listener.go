package s3api

import (
	"net"
	"time"
)

// deadlineListener sets a read deadline on every accepted connection. Hertz
// only applies its read timeout from the second request of a keep-alive
// connection, so without this a client could hold the first request's head
// open indefinitely.
type deadlineListener struct {
	net.Listener
	headerTimeout time.Duration
}

// Accept returns the next connection with the request-head deadline set.
func (l deadlineListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(l.headerTimeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}
