package xmldec

import (
	"encoding/base64"
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// ParseCompleteMultipartUpload decodes the body of CompleteMultipartUpload.
// Parts must be non-empty, have part numbers in 1..object.MaxPartNumber and
// be strictly ascending (InvalidPartOrder otherwise). ETags are returned
// without quotes. Each part may carry one additional checksum.
func ParseCompleteMultipartUpload(data []byte) ([]object.CompletePart, error) {
	d := newDecoder(data)
	if err := expectRoot(d, "CompleteMultipartUpload"); err != nil {
		return nil, err
	}
	var parts []object.CompletePart
	for {
		tok, err := next(d)
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "Part" {
				return nil, malformed()
			}
			if len(parts) >= object.MaxPartNumber {
				return nil, malformed()
			}
			p, err := parseCompletePart(d)
			if err != nil {
				return nil, err
			}
			if n := len(parts); n > 0 && p.PartNumber <= parts[n-1].PartNumber {
				return nil, s3err.Errorf(s3err.InvalidPartOrder, "the list of parts was not in ascending order")
			}
			parts = append(parts, p)
		case xml.EndElement:
			if len(parts) == 0 {
				return nil, malformed()
			}
			return parts, expectEnd(d)
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return nil, malformed()
			}
		default:
			return nil, malformed()
		}
	}
}

// parseCompletePart decodes one <Part> element; its start tag is consumed.
func parseCompletePart(d *xml.Decoder) (object.CompletePart, error) {
	var (
		p         object.CompletePart
		havePart  bool
		haveCheck bool
	)
	for {
		tok, err := next(d)
		if err != nil {
			return p, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			if name == "PartNumber" {
				if havePart {
					return p, malformed()
				}
				havePart = true
				text, err := readText(d)
				if err != nil {
					return p, err
				}
				n, err := strconv.Atoi(strings.TrimSpace(text))
				if err != nil || n < 1 || n > object.MaxPartNumber {
					return p, s3err.Errorf(s3err.InvalidArgument, "part number must be an integer between 1 and %d", object.MaxPartNumber)
				}
				p.PartNumber = n
				continue
			}
			if name == "ETag" {
				text, err := readText(d)
				if err != nil {
					return p, err
				}
				p.ETag = unquoteETag(text)
				continue
			}
			algo, ok := checksumElements[name]
			if !ok || haveCheck {
				return p, malformed()
			}
			haveCheck = true
			text, err := readText(d)
			if err != nil {
				return p, err
			}
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
			if err != nil || len(raw) != digestLength[algo] {
				return p, malformed()
			}
			p.Checksum = object.Checksum{Algorithm: algo, Value: raw}
		case xml.EndElement:
			if !havePart {
				return p, malformed()
			}
			return p, nil
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return p, malformed()
			}
		default:
			return p, malformed()
		}
	}
}
