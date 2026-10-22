package sigv4

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	// maxChunkSize bounds one aws-chunked chunk. Clients send 64 KiB chunks
	// by default; the bound keeps a hostile size from being trusted.
	maxChunkSize = 16 << 20
	// maxChunkHeaderLen bounds a chunk header line, which is about 100 bytes
	// when signed.
	maxChunkHeaderLen = 256
	// chunkBufferSize is the reader's buffer. Reads of at least this size
	// bypass it and fill the caller's buffer directly.
	chunkBufferSize = 8 << 10
	// chunkSigPrefix introduces the signature extension of a chunk header.
	chunkSigPrefix = ";chunk-signature="
)

// chunkedConfig is what the decoder needs from a verified request.
type chunkedConfig struct {
	kind payloadKind
	// key is the SigV4 signing key for the request date and scope.
	key [sha256.Size]byte
	// amzDate is the request time in ISO 8601 basic format.
	amzDate string
	// scope is the credential scope without the access key.
	scope string
	// seed is the request signature, which starts the chunk chain.
	seed [sha256.Size]byte
	// decodedLen is x-amz-decoded-content-length.
	decodedLen int64
	// declared lists the trailer names from x-amz-trailer, lower-case.
	declared []string
}

// chunkedReader decodes an aws-chunked body and verifies it as it streams.
//
// A chunk's data reaches the caller before the chunk is complete, because a
// signature covers the whole chunk. The final bytes of each chunk are
// returned only after the chunk signature has verified, and any failure
// is returned instead of io.EOF. Callers must therefore never commit a body
// that errored. Memory use is bounded by the read buffer, never by the
// body size.
type chunkedReader struct {
	cfg chunkedConfig
	br  *bufio.Reader
	// mac is an HMAC keyed with the signing key, reset for every signature.
	mac hash.Hash
	// dataHash hashes the current chunk's data.
	dataHash hash.Hash
	// buf is scratch space for building strings to sign.
	buf []byte
	// prev is the signature of the previous chunk, the seed at first.
	prev [sha256.Size]byte
	// pending is the signature the current chunk header declared.
	pending [sha256.Size]byte
	// remaining is the number of data bytes left in the current chunk.
	remaining int64
	// total is the number of decoded bytes returned so far.
	total int64
	// done is set after the final chunk and its trailer verified.
	done bool
	// err is sticky: once set, every Read returns it.
	err error
	// trailer is the verified trailing checksum, when hasTrailer is set.
	trailer    object.Checksum
	hasTrailer bool
}

// newChunkedReader returns a decoder over body.
func newChunkedReader(body io.Reader, cfg chunkedConfig) *chunkedReader {
	return &chunkedReader{
		cfg:      cfg,
		br:       bufio.NewReaderSize(body, chunkBufferSize),
		mac:      hmac.New(sha256.New, cfg.key[:]),
		dataHash: sha256.New(),
		prev:     cfg.seed,
	}
}

// signed reports whether chunks carry signatures.
func (c *chunkedReader) signed() bool {
	return c.cfg.kind != payloadStreamingUnsignedTrailer
}

// Read implements io.Reader. It returns io.EOF only after the whole stream
// verified and the decoded length matched.
func (c *chunkedReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	for c.remaining == 0 {
		if c.done {
			c.err = io.EOF
			return 0, io.EOF
		}
		if err := c.nextChunk(); err != nil {
			c.err = err
			return 0, err
		}
	}
	return c.readData(p)
}

// readData copies chunk data straight into p and verifies the chunk when its
// last byte has been read.
func (c *chunkedReader) readData(p []byte) (int, error) {
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.br.Read(p)
	c.dataHash.Write(p[:n])
	c.remaining -= int64(n)
	c.total += int64(n)
	if err != nil {
		c.err = readError(err)
		return 0, c.err
	}
	if c.remaining == 0 {
		if err := c.finishChunk(); err != nil {
			c.err = err
			return 0, err
		}
	}
	return n, nil
}

// nextChunk reads the next chunk header. A zero-size chunk ends the stream.
func (c *chunkedReader) nextChunk() error {
	line, err := c.readLine(maxChunkHeaderLen)
	if err != nil {
		return err
	}
	size, sig, err := c.parseChunkHeader(line)
	if err != nil {
		return err
	}
	if size == 0 {
		return c.finalChunk(sig)
	}
	if c.total+size > c.cfg.decodedLen {
		return framing("data exceeds x-amz-decoded-content-length")
	}
	c.remaining = size
	c.pending = sig
	c.dataHash.Reset()
	return nil
}

// finishChunk consumes the CRLF after a chunk's data and verifies its
// signature.
func (c *chunkedReader) finishChunk() error {
	if err := c.expectCRLF(); err != nil {
		return err
	}
	if c.signed() {
		want := c.chunkSignature(sumDigest(c.dataHash))
		if !hmac.Equal(want[:], c.pending[:]) {
			return errSignature()
		}
		c.prev = want
	}
	return nil
}

// finalChunk handles the zero-size chunk: it verifies the decoded length,
// the signature and the framing that follows.
func (c *chunkedReader) finalChunk(sig [sha256.Size]byte) error {
	if c.signed() {
		want := c.chunkSignature(emptyDigest)
		if !hmac.Equal(want[:], sig[:]) {
			return errSignature()
		}
		c.prev = want
	}
	switch c.cfg.kind {
	case payloadStreamingSigned:
		if err := c.expectCRLF(); err != nil {
			return err
		}
	default:
		if err := c.readTrailers(); err != nil {
			return err
		}
	}
	// The length check comes after the signatures so that a dropped or
	// truncated chunk is reported as the tampering it is.
	if c.total != c.cfg.decodedLen {
		return s3err.Errorf(s3err.IncompleteBody, "body is shorter than x-amz-decoded-content-length")
	}
	if err := c.checkEnd(); err != nil {
		return err
	}
	c.done = true
	return nil
}

// checkEnd rejects bytes after the final chunk. The transport delimits the
// body, so anything left over is a framing error.
func (c *chunkedReader) checkEnd() error {
	_, err := c.br.Peek(1)
	if err == nil {
		return framing("unexpected data after the final chunk")
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	return readError(err)
}

// parseChunkHeader parses "<hex size>" for unsigned chunks, or
// "<hex size>;chunk-signature=<64 hex>" for signed chunks. Sizes must be
// lower-case hex without leading zeros and at most maxChunkSize.
func (c *chunkedReader) parseChunkHeader(line []byte) (int64, [sha256.Size]byte, error) {
	var sig [sha256.Size]byte
	sizeHex, ext := line, []byte(nil)
	if i := bytes.IndexByte(line, ';'); i >= 0 {
		sizeHex, ext = line[:i], line[i:]
	}
	size, err := parseChunkSize(sizeHex)
	if err != nil {
		return 0, sig, err
	}
	if !c.signed() {
		if ext != nil {
			return 0, sig, framing("unexpected chunk extension")
		}
		return size, sig, nil
	}
	if !bytes.HasPrefix(ext, []byte(chunkSigPrefix)) {
		return 0, sig, framing("missing chunk-signature")
	}
	sig, ok := decodeSignature(ext[len(chunkSigPrefix):])
	if !ok {
		return 0, sig, framing("malformed chunk-signature")
	}
	return size, sig, nil
}

// parseChunkSize parses the hexadecimal chunk size.
func parseChunkSize(b []byte) (int64, error) {
	if len(b) == 0 || len(b) > 8 {
		return 0, framing("malformed chunk size")
	}
	if len(b) > 1 && b[0] == '0' {
		return 0, framing("chunk size has leading zeros")
	}
	var n int64
	for _, c := range b {
		v, ok := lowerHexValue(c)
		if !ok {
			return 0, framing("malformed chunk size")
		}
		n = n<<4 | int64(v)
	}
	if n > maxChunkSize {
		return 0, framing("chunk exceeds the maximum size")
	}
	return n, nil
}

// lowerHexValue returns the value of a lower-case hex digit.
func lowerHexValue(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

// decodeSignature decodes exactly 64 lower-case hex characters.
func decodeSignature(b []byte) ([sha256.Size]byte, bool) {
	var out [sha256.Size]byte
	if len(b) != 2*sha256.Size {
		return out, false
	}
	for _, c := range b {
		if _, ok := lowerHexValue(c); !ok {
			return out, false
		}
	}
	if _, err := hex.Decode(out[:], b); err != nil {
		return out, false
	}
	return out, true
}

// chunkSignature computes the signature of a chunk from the hash of its
// data and the previous signature:
//
//	AWS4-HMAC-SHA256-PAYLOAD\n<date>\n<scope>\n<prev>\n<empty hash>\n<data hash>
func (c *chunkedReader) chunkSignature(data [sha256.Size]byte) [sha256.Size]byte {
	b := c.buf[:0]
	b = append(b, "AWS4-HMAC-SHA256-PAYLOAD\n"...)
	b = append(b, c.cfg.amzDate...)
	b = append(b, '\n')
	b = append(b, c.cfg.scope...)
	b = append(b, '\n')
	b = hex.AppendEncode(b, c.prev[:])
	b = append(b, '\n')
	b = append(b, emptyPayloadSHA256...)
	b = append(b, '\n')
	b = hex.AppendEncode(b, data[:])
	c.buf = b
	return c.sign(b)
}

// sign returns HMAC-SHA256 of sts under the signing key.
func (c *chunkedReader) sign(sts []byte) [sha256.Size]byte {
	c.mac.Reset()
	c.mac.Write(sts)
	var out [sha256.Size]byte
	c.mac.Sum(out[:0])
	return out
}

// readLine returns the next line without its CRLF. The slice is valid only
// until the next read. A line longer than max is malformed.
func (c *chunkedReader) readLine(max int) ([]byte, error) {
	line, err := c.br.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, framing("line too long")
		}
		return nil, readError(err)
	}
	if len(line) > max+2 {
		return nil, framing("line too long")
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, framing("line is not terminated by CRLF")
	}
	return line[:len(line)-2], nil
}

// expectCRLF consumes a CRLF.
func (c *chunkedReader) expectCRLF() error {
	var b [2]byte
	if _, err := io.ReadFull(c.br, b[:]); err != nil {
		return readError(err)
	}
	if b[0] != '\r' || b[1] != '\n' {
		return framing("expected CRLF")
	}
	return nil
}

// trailerChecksum returns the decoded trailing checksum of this stream.
func (c *chunkedReader) trailerChecksum() (object.Checksum, bool) {
	return c.trailer, c.hasTrailer
}

// readError maps an error from the body to a client-visible error. A body
// that ends early is IncompleteBody; other failures keep their cause.
func readError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return s3err.Errorf(s3err.IncompleteBody, "the request body ended before the aws-chunked stream was complete")
	}
	return fmt.Errorf("sigv4: read aws-chunked body: %w", err)
}

// framing returns the error for malformed aws-chunked framing.
func framing(msg string) error {
	return s3err.Errorf(s3err.InvalidRequest, "malformed aws-chunked body: %s", msg)
}

// errSignature is the error for a chunk or trailer that does not verify.
func errSignature() error {
	return s3err.Errorf(s3err.SignatureDoesNotMatch, "a chunk signature does not match")
}

// sumDigest returns the digest of h.
func sumDigest(h hash.Hash) [sha256.Size]byte {
	var out [sha256.Size]byte
	h.Sum(out[:0])
	return out
}
