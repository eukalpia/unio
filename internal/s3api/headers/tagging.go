package headers

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

const (
	// maxTagKeyLen bounds a decoded tag key in bytes.
	maxTagKeyLen = 128
	// maxTagValueLen bounds a decoded tag value in bytes.
	maxTagValueLen = 256
	// maxTaggingLen bounds the raw x-amz-tagging value. Percent-encoding
	// expands each byte to at most three, so this covers every valid tag set.
	maxTaggingLen = object.MaxTags * (3*maxTagKeyLen + 3*maxTagValueLen + len("=&"))
)

// ParseTagging parses the x-amz-tagging header: a URL query-encoded tag set.
// It returns the canonical encoding (keys sorted, as url.Values.Encode
// produces). Errors return InvalidTag: malformed encoding, duplicate keys,
// empty keys, empty pairs, more than object.MaxTags tags, a key longer than
// 128 bytes, a value longer than 256 bytes, invalid UTF-8 and control bytes.
// Empty input returns "", nil.
func ParseTagging(v string) (string, error) {
	if v == "" {
		return "", nil
	}
	if len(v) > maxTaggingLen {
		return "", s3err.Errorf(s3err.InvalidTag, "tag set is too long")
	}
	tags := make(url.Values)
	count := 0
	rest := v
	for {
		pair, more, found := strings.Cut(rest, "&")
		if pair == "" {
			return "", s3err.Errorf(s3err.InvalidTag, "tag set has an empty tag")
		}
		count++
		if count > object.MaxTags {
			return "", s3err.Errorf(s3err.InvalidTag, "tag set has more than %d tags", object.MaxTags)
		}
		key, value, err := parseTagPair(pair)
		if err != nil {
			return "", err
		}
		if _, dup := tags[key]; dup {
			return "", s3err.Errorf(s3err.InvalidTag, "tag key is repeated")
		}
		tags.Set(key, value)
		if !found {
			break
		}
		rest = more
	}
	return tags.Encode(), nil
}

// parseTagPair decodes one key=value pair of a tag set and checks its bounds.
func parseTagPair(pair string) (string, string, error) {
	rawKey, rawValue, _ := strings.Cut(pair, "=")
	key, err := url.QueryUnescape(rawKey)
	if err != nil {
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag key is not valid URL encoding")
	}
	value, err := url.QueryUnescape(rawValue)
	if err != nil {
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag value is not valid URL encoding")
	}
	switch {
	case key == "":
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag key must not be empty")
	case len(key) > maxTagKeyLen:
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag key is longer than %d bytes", maxTagKeyLen)
	case len(value) > maxTagValueLen:
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag value is longer than %d bytes", maxTagValueLen)
	case !utf8.ValidString(key) || !utf8.ValidString(value):
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag is not valid UTF-8")
	case containsControl(key) || containsControl(value):
		return "", "", s3err.Errorf(s3err.InvalidTag, "tag contains a control character")
	}
	return key, value, nil
}
