package xmldec

import (
	"encoding/xml"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// ParseTagging decodes the body of PutObjectTagging: a Tagging document with
// one TagSet, whose Tag elements each hold a Key and a Value. An empty TagSet
// is valid and removes every tag. Structural errors are MalformedXML. The tag
// rules are not checked here; object.ValidateTags checks the set in the object
// layer, for every path that stores tags. Reading stops at object.MaxTags+1
// tags, so that the size of the result is bounded by the body limit and by
// the tag count.
func ParseTagging(data []byte) (object.TagSet, error) {
	d := newDecoder(data)
	if err := expectRoot(d, "Tagging"); err != nil {
		return nil, err
	}
	var tags object.TagSet
	seen := false
	for {
		tok, err := next(d)
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "TagSet" || seen {
				return nil, malformed()
			}
			seen = true
			if tags, err = parseTagSet(d); err != nil {
				return nil, err
			}
		case xml.EndElement:
			if !seen {
				return nil, malformed()
			}
			return tags, expectEnd(d)
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return nil, malformed()
			}
		default:
			return nil, malformed()
		}
	}
}

// parseTagSet decodes the Tag elements of a TagSet; its start tag is consumed
// and so is its end tag.
func parseTagSet(d *xml.Decoder) (object.TagSet, error) {
	var tags object.TagSet
	for {
		tok, err := next(d)
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local != "Tag" {
				return nil, malformed()
			}
			if len(tags) == object.MaxTags {
				return nil, s3err.Errorf(s3err.InvalidTag, "tag set has more than %d tags", object.MaxTags)
			}
			tag, err := parseTag(d)
			if err != nil {
				return nil, err
			}
			tags = append(tags, tag)
		case xml.EndElement:
			return tags, nil
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return nil, malformed()
			}
		default:
			return nil, malformed()
		}
	}
}

// parseTag decodes one Tag element; its start tag is consumed. The Key and
// the Value are each required exactly once. Their text is kept as it is, with
// no trimming, because a key is compared byte for byte.
func parseTag(d *xml.Decoder) (object.Tag, error) {
	var tag object.Tag
	haveKey, haveValue := false, false
	for {
		tok, err := next(d)
		if err != nil {
			return tag, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Key":
				if haveKey {
					return tag, malformed()
				}
				haveKey = true
				if tag.Key, err = readText(d); err != nil {
					return tag, err
				}
			case "Value":
				if haveValue {
					return tag, malformed()
				}
				haveValue = true
				if tag.Value, err = readText(d); err != nil {
					return tag, err
				}
			default:
				return tag, malformed()
			}
		case xml.EndElement:
			if !haveKey || !haveValue {
				return tag, malformed()
			}
			return tag, nil
		case xml.CharData:
			if len(strings.TrimSpace(string(t))) != 0 {
				return tag, malformed()
			}
		default:
			return tag, malformed()
		}
	}
}
