package sigv4

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/eukalpia/unio/internal/object"
)

const (
	// maxTrailerLineLen bounds one trailer line. The longest supported
	// checksum value is a base64 SHA-256 digest of 44 characters.
	maxTrailerLineLen = 512
	// maxTrailerLines bounds the trailer section. At most one checksum and
	// one signature are valid, so a few lines are enough.
	maxTrailerLines = 8
	// trailerSignatureName is the header that carries the trailer signature,
	// in lower case. Field names are matched case-insensitively.
	trailerSignatureName = "x-amz-trailer-signature"
)

// trailerChecksum maps a lower-case trailing checksum header name to its
// algorithm and digest size. Unknown names return false.
func trailerChecksum(name string) (object.ChecksumAlgorithm, int, bool) {
	switch name {
	case "x-amz-checksum-crc32":
		return object.ChecksumCRC32, 4, true
	case "x-amz-checksum-crc32c":
		return object.ChecksumCRC32C, 4, true
	case "x-amz-checksum-crc64nvme":
		return object.ChecksumCRC64NVME, 8, true
	case "x-amz-checksum-sha1":
		return object.ChecksumSHA1, 20, true
	case "x-amz-checksum-sha256":
		return object.ChecksumSHA256, 32, true
	}
	return object.ChecksumNone, 0, false
}

// parseTrailerField splits one trailer line, without its CRLF, into a
// lower-case field name and a trimmed value.
//
// Field names are case-insensitive and may contain only letters, digits and
// hyphens, so whitespace before the colon is refused. The value may carry
// optional whitespace (spaces or tabs) on either side, which HTTP ignores.
// It is trimmed here, so that the decoded checksum and the canonical trailer
// both see the same bytes. The canonical trailer uses lower-case names and
// trimmed values, as SigV4 canonicalizes headers.
func parseTrailerField(line []byte) (string, []byte, error) {
	rawName, value, ok := cutBytes(line, ':')
	if !ok {
		return "", nil, framing("malformed trailer line")
	}
	if len(rawName) == 0 || !allBytes(rawName, isFieldNameByte) {
		return "", nil, framing("malformed trailer name")
	}
	return strings.ToLower(string(rawName)), bytes.Trim(value, " \t"), nil
}

// isFieldNameByte reports whether c may appear in a trailer field name.
func isFieldNameByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-'
}

// allBytes reports whether every byte of b satisfies ok.
func allBytes(b []byte, ok func(byte) bool) bool {
	for _, c := range b {
		if !ok(c) {
			return false
		}
	}
	return true
}

// readTrailers parses the trailer section that follows the final chunk. It
// accepts at most one checksum header, whose name must match x-amz-trailer,
// and, for signed trailers, a final x-amz-trailer-signature line. The
// signature covers the canonical checksum line, chained from the final
// chunk's signature.
func (c *chunkedReader) readTrailers() error {
	signed := c.cfg.kind == payloadStreamingTrailer
	canon := c.buf[:0]
	var (
		checksumName string
		sig          [sha256.Size]byte
		sigSeen      bool
		lines        int
	)
	for {
		line, err := c.readLine(maxTrailerLineLen)
		if err != nil {
			return err
		}
		if len(line) == 0 {
			break
		}
		lines++
		if lines > maxTrailerLines {
			return framing("too many trailer lines")
		}
		if sigSeen {
			return framing("trailer signature must be the last trailer")
		}
		name, value, err := parseTrailerField(line)
		if err != nil {
			return err
		}
		if signed && name == trailerSignatureName {
			s, ok := decodeSignature(value)
			if !ok {
				return framing("malformed trailer signature")
			}
			sig, sigSeen = s, true
			continue
		}
		if err := c.acceptChecksum(name, value, &checksumName); err != nil {
			return err
		}
		canon = append(canon, name...)
		canon = append(canon, ':')
		canon = append(canon, value...)
		canon = append(canon, '\n')
	}
	c.buf = canon
	if signed {
		if !sigSeen {
			return framing("missing trailer signature")
		}
		want := c.trailerSignature(canon)
		if !hmac.Equal(want[:], sig[:]) {
			return errSignature()
		}
	} else if sigSeen {
		return framing("unexpected trailer signature")
	}
	return c.checkDeclared(checksumName)
}

// acceptChecksum decodes a trailing checksum and records it. A second
// checksum, an unknown header, or a value of the wrong length is malformed.
// The value must already be trimmed.
func (c *chunkedReader) acceptChecksum(name string, value []byte, recorded *string) error {
	alg, size, ok := trailerChecksum(name)
	if !ok || *recorded != "" {
		return framing("unsupported or duplicate trailer")
	}
	raw, err := base64.StdEncoding.DecodeString(string(value))
	if err != nil || len(raw) != size {
		return framing("malformed trailing checksum")
	}
	c.trailer = object.Checksum{Algorithm: alg, Type: object.ChecksumFullObject, Value: raw}
	c.hasTrailer = true
	*recorded = name
	return nil
}

// checkDeclared requires the checksum trailer to be the one that the
// x-amz-trailer header announced, and no other.
func (c *chunkedReader) checkDeclared(checksumName string) error {
	if !c.hasTrailer {
		if len(c.cfg.declared) != 0 {
			return framing("announced trailer was not sent")
		}
		return nil
	}
	if len(c.cfg.declared) != 1 || c.cfg.declared[0] != checksumName {
		return framing("trailer does not match x-amz-trailer")
	}
	return nil
}

// trailerSignature computes the signature of the trailer section:
//
//	AWS4-HMAC-SHA256-TRAILER\n<date>\n<scope>\n<prev>\n<hash of canonical trailers>
func (c *chunkedReader) trailerSignature(canon []byte) [sha256.Size]byte {
	ch := sha256.Sum256(canon)
	b := c.buf[:0]
	b = append(b, "AWS4-HMAC-SHA256-TRAILER\n"...)
	b = append(b, c.cfg.amzDate...)
	b = append(b, '\n')
	b = append(b, c.cfg.scope...)
	b = append(b, '\n')
	b = hex.AppendEncode(b, c.prev[:])
	b = append(b, '\n')
	b = hex.AppendEncode(b, ch[:])
	c.buf = b
	return c.sign(b)
}

// cutBytes splits b around the first sep.
func cutBytes(b []byte, sep byte) (before, after []byte, found bool) {
	i := bytes.IndexByte(b, sep)
	if i < 0 {
		return b, nil, false
	}
	return b[:i], b[i+1:], true
}
