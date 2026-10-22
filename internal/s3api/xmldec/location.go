package xmldec

import (
	"encoding/xml"
	"strings"
)

// ParseCreateBucketConfiguration decodes the optional CreateBucket body and
// returns the requested LocationConstraint. An empty body returns "", nil,
// meaning the server's default region.
func ParseCreateBucketConfiguration(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	d := newDecoder(data)
	if err := expectRoot(d, "CreateBucketConfiguration"); err != nil {
		return "", err
	}
	location := ""
	seen := false
	for {
		tok, err := next(d)
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "LocationConstraint" || seen {
				return "", malformed()
			}
			seen = true
			if location, err = readText(d); err != nil {
				return "", err
			}
		case xml.EndElement:
			return strings.TrimSpace(location), expectEnd(d)
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return "", malformed()
			}
		default:
			return "", malformed()
		}
	}
}
