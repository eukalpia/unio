package xmldec

import (
	"encoding/xml"
	"strings"

	"github.com/eukalpia/unio/internal/object"
)

// DeleteRequest is a decoded DeleteObjects body.
type DeleteRequest struct {
	// Quiet suppresses the per-key success entries in the response.
	Quiet bool
	// Objects are the keys to delete, in request order. Keys are not
	// trimmed: leading and trailing spaces are part of the key.
	Objects []object.ToDelete
}

// ParseDeleteObjects decodes the body of DeleteObjects. It requires between
// one and object.MaxDeleteObjects objects, each with a Key element and an
// optional VersionId. Key validity is checked per key by the object layer.
func ParseDeleteObjects(data []byte) (DeleteRequest, error) {
	var req DeleteRequest
	d := newDecoder(data)
	if err := expectRoot(d, "Delete"); err != nil {
		return req, err
	}
	for {
		tok, err := next(d)
		if err != nil {
			return req, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Quiet":
				text, err := readText(d)
				if err != nil {
					return req, err
				}
				switch strings.TrimSpace(text) {
				case "true":
					req.Quiet = true
				case "false":
				default:
					return req, malformed()
				}
			case "Object":
				if len(req.Objects) >= object.MaxDeleteObjects {
					return req, malformed()
				}
				o, err := parseDeleteObject(d)
				if err != nil {
					return req, err
				}
				req.Objects = append(req.Objects, o)
			default:
				return req, malformed()
			}
		case xml.EndElement:
			if len(req.Objects) == 0 {
				return req, malformed()
			}
			return req, expectEnd(d)
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return req, malformed()
			}
		default:
			return req, malformed()
		}
	}
}

// parseDeleteObject decodes one <Object> element; its start tag is consumed.
func parseDeleteObject(d *xml.Decoder) (object.ToDelete, error) {
	var o object.ToDelete
	haveKey := false
	for {
		tok, err := next(d)
		if err != nil {
			return o, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Key":
				if haveKey {
					return o, malformed()
				}
				haveKey = true
				if o.Key, err = readText(d); err != nil {
					return o, err
				}
			case "VersionId":
				if o.VersionID != "" {
					return o, malformed()
				}
				if o.VersionID, err = readText(d); err != nil {
					return o, err
				}
				if o.VersionID == "" {
					return o, malformed()
				}
			default:
				return o, malformed()
			}
		case xml.EndElement:
			if !haveKey {
				return o, malformed()
			}
			return o, nil
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return o, malformed()
			}
		default:
			return o, malformed()
		}
	}
}
