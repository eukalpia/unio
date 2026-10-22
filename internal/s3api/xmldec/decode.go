// Package xmldec decodes the small XML request bodies of S3 operations
// (CompleteMultipartUpload, DeleteObjects, CreateBucket configuration). The
// decoding is strict: unknown elements, DTD directives, nesting where text
// is expected and trailing content are rejected with MalformedXML. Bodies
// are read under the hard limit object.MaxXMLBodySize.
package xmldec

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// malformed is the client-visible error for any structurally invalid body.
func malformed() error {
	return s3err.Errorf(s3err.MalformedXML, "The XML you provided was not well-formed or did not validate against our published schema.")
}

// newDecoder returns a strict decoder over data. The entity map is nil, so
// custom entities are rejected by the parser.
func newDecoder(data []byte) *xml.Decoder {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	return d
}

// next returns the next significant token. Comments and processing
// instructions are skipped; directives (DOCTYPE and friends) are rejected.
func next(d *xml.Decoder) (xml.Token, error) {
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, malformed()
		}
		switch tok.(type) {
		case xml.Comment, xml.ProcInst:
			continue
		case xml.Directive:
			return nil, malformed()
		}
		return tok, nil
	}
}

// expectRoot consumes the root start element, which must have the given
// local name. White space before the root is allowed.
func expectRoot(d *xml.Decoder, name string) error {
	for {
		tok, err := next(d)
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return malformed()
			}
		case xml.StartElement:
			if t.Name.Local != name {
				return malformed()
			}
			return nil
		default:
			return malformed()
		}
	}
}

// expectEnd verifies that only white space follows the root element.
func expectEnd(d *xml.Decoder) error {
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return malformed()
		}
		switch t := tok.(type) {
		case xml.CharData:
			if len(bytes.TrimSpace(t)) != 0 {
				return malformed()
			}
		case xml.Comment, xml.ProcInst:
		default:
			return malformed()
		}
	}
}

// readText returns the character data of the element whose start tag was
// just consumed, and consumes its end tag. Nested elements are rejected.
func readText(d *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := next(d)
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			return b.String(), nil
		default:
			return "", malformed()
		}
	}
}

// checksumElements maps the checksum element names used in request bodies
// to their algorithms.
var checksumElements = map[string]object.ChecksumAlgorithm{
	"ChecksumCRC32":     object.ChecksumCRC32,
	"ChecksumCRC32C":    object.ChecksumCRC32C,
	"ChecksumCRC64NVME": object.ChecksumCRC64NVME,
	"ChecksumSHA1":      object.ChecksumSHA1,
	"ChecksumSHA256":    object.ChecksumSHA256,
}

// digestLength is the raw digest length of each algorithm.
var digestLength = map[object.ChecksumAlgorithm]int{
	object.ChecksumCRC32:     4,
	object.ChecksumCRC32C:    4,
	object.ChecksumCRC64NVME: 8,
	object.ChecksumSHA1:      20,
	object.ChecksumSHA256:    32,
}

// unquoteETag removes surrounding double quotes from an ETag.
func unquoteETag(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}
