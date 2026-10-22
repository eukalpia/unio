package engine

import (
	"fmt"
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// maxEmptyReads bounds consecutive (0, nil) reads from a body.
const maxEmptyReads = 100

// bodyGuard reads a request body on behalf of a write and records how the
// body ended. The object layer commits only after finish has seen a clean
// end (invariant I4): io.EOF after exactly Size() bytes, and no error on
// the way. An error reported by Read is never hidden, even when the
// erasure set swallowed it.
type bodyGuard struct {
	body object.Body
	n    int64 // bytes handed out so far
	eof  bool  // io.EOF has been observed
	err  error // first error other than io.EOF; sticky
}

func newBodyGuard(body object.Body) *bodyGuard {
	return &bodyGuard{body: body}
}

// Read implements io.Reader.
func (g *bodyGuard) Read(p []byte) (int, error) {
	if g.err != nil {
		return 0, g.err
	}
	n, err := g.body.Read(p)
	g.n += int64(n)
	switch {
	case err == io.EOF:
		g.eof = true
	case err != nil:
		g.err = err
	}
	return n, err
}

// finish confirms that the body ended cleanly with exactly Size() bytes. If
// io.EOF has not been seen yet, it reads once more to look for it. It returns
// the body's own error when the body failed, so that the S3 code it carries
// reaches the client.
func (g *bodyGuard) finish() error {
	if g.err != nil {
		return g.err
	}
	if !g.eof {
		var one [1]byte
		for i := 0; ; i++ {
			n, err := g.body.Read(one[:])
			g.n += int64(n)
			if n > 0 {
				return s3err.Errorf(s3err.IncompleteBody, "request body is longer than declared")
			}
			if err == io.EOF {
				g.eof = true
				break
			}
			if err != nil {
				g.err = err
				return err
			}
			if i >= maxEmptyReads {
				return fmt.Errorf("engine: reading request body: %w", io.ErrNoProgress)
			}
		}
	}
	if g.n != g.body.Size() {
		return s3err.Errorf(s3err.IncompleteBody, "request body has %d bytes, declared %d", g.n, g.body.Size())
	}
	return nil
}
