package object

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/eukalpia/unio/internal/s3err"
)

// ReservedTagPrefix is the prefix of system tags. Clients may not set,
// change or delete a tag whose key starts with it.
const ReservedTagPrefix = "aws:"

// Tag is one user-defined tag of an object version.
type Tag struct {
	Key   string
	Value string
}

// TagSet is the tag set of one object version. Keys are unique and the set
// holds at most MaxTags tags; ValidateTags checks both rules.
type TagSet []Tag

// ValidateTags checks a tag set against the S3 rules. Every tag set reaches
// the store through this check, whether it came from the x-amz-tagging header
// or from a Tagging document, so the two paths cannot disagree.
//
// Keys are 1 to MaxTagKeyLength characters and values are 0 to
// MaxTagValueLength characters, where a character is a Unicode code point.
// Keys must be unique and must not use ReservedTagPrefix, compared without
// regard to case. Tags must be valid UTF-8 without control characters. Each
// violation is an InvalidTag error that does not repeat the tag's content.
func ValidateTags(tags TagSet) error {
	if len(tags) > MaxTags {
		return s3err.Errorf(s3err.InvalidTag, "tag set has more than %d tags", MaxTags)
	}
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		if err := validateTag(t); err != nil {
			return err
		}
		if _, dup := seen[t.Key]; dup {
			return s3err.Errorf(s3err.InvalidTag, "tag keys must be unique")
		}
		seen[t.Key] = struct{}{}
	}
	return nil
}

// validateTag checks the bounds, encoding and reserved prefix of one tag.
func validateTag(t Tag) error {
	if !utf8.ValidString(t.Key) || !utf8.ValidString(t.Value) {
		return s3err.Errorf(s3err.InvalidTag, "tag is not valid UTF-8")
	}
	if hasControl(t.Key) || hasControl(t.Value) {
		return s3err.Errorf(s3err.InvalidTag, "tag contains a control character")
	}
	switch n := utf8.RuneCountInString(t.Key); {
	case n == 0:
		return s3err.Errorf(s3err.InvalidTag, "tag key must not be empty")
	case n > MaxTagKeyLength:
		return s3err.Errorf(s3err.InvalidTag, "tag key is longer than %d characters", MaxTagKeyLength)
	}
	if utf8.RuneCountInString(t.Value) > MaxTagValueLength {
		return s3err.Errorf(s3err.InvalidTag, "tag value is longer than %d characters", MaxTagValueLength)
	}
	if hasReservedPrefix(t.Key) {
		return s3err.Errorf(s3err.InvalidTag, "tag key uses the reserved prefix %s", ReservedTagPrefix)
	}
	return nil
}

// hasReservedPrefix reports whether key starts with ReservedTagPrefix. Only
// ASCII letters are folded to lower case, so no Unicode character matches.
func hasReservedPrefix(key string) bool {
	if len(key) < len(ReservedTagPrefix) {
		return false
	}
	for i := 0; i < len(ReservedTagPrefix); i++ {
		c := key[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != ReservedTagPrefix[i] {
			return false
		}
	}
	return true
}

// hasControl reports whether s holds a C0 control character or DEL.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// EncodeTags returns the stored form of a tag set: the URL query encoding of
// the x-amz-tagging header, with the keys in sorted order. An empty set
// encodes as "". EncodeTags does not validate; call ValidateTags first.
func EncodeTags(tags TagSet) string {
	if len(tags) == 0 {
		return ""
	}
	v := make(url.Values, len(tags))
	for _, t := range tags {
		v.Set(t.Key, t.Value)
	}
	return v.Encode()
}

// DecodeTags parses a tag set in the URL query encoding of EncodeTags, which
// is also the encoding of the x-amz-tagging header, and validates it with
// ValidateTags. The empty string is the empty tag set. A pair without "="
// has an empty value, and "+" decodes to a space. Errors are InvalidTag.
func DecodeTags(encoded string) (TagSet, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > MaxEncodedTagsLength {
		return nil, s3err.Errorf(s3err.InvalidTag, "tag set is too long")
	}
	var tags TagSet
	rest := encoded
	for {
		pair, more, found := strings.Cut(rest, "&")
		if pair == "" {
			return nil, s3err.Errorf(s3err.InvalidTag, "tag set has an empty tag")
		}
		if len(tags) == MaxTags {
			return nil, s3err.Errorf(s3err.InvalidTag, "tag set has more than %d tags", MaxTags)
		}
		t, err := parseTagPair(pair)
		if err != nil {
			return nil, err
		}
		tags = append(tags, t)
		if !found {
			break
		}
		rest = more
	}
	if err := ValidateTags(tags); err != nil {
		return nil, err
	}
	return tags, nil
}

// parseTagPair decodes one key=value pair of the URL query encoding.
func parseTagPair(pair string) (Tag, error) {
	rawKey, rawValue, _ := strings.Cut(pair, "=")
	key, err := url.QueryUnescape(rawKey)
	if err != nil {
		return Tag{}, s3err.Errorf(s3err.InvalidTag, "tag key is not valid URL encoding")
	}
	value, err := url.QueryUnescape(rawValue)
	if err != nil {
		return Tag{}, s3err.Errorf(s3err.InvalidTag, "tag value is not valid URL encoding")
	}
	return Tag{Key: key, Value: value}, nil
}
