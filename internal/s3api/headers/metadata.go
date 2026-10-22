package headers

import (
	"net/http"
	"strings"

	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// userMetadataPrefix starts the user metadata headers.
const userMetadataPrefix = "x-amz-meta-"

// ParseUserMetadata collects x-amz-meta-* headers into a map keyed by the
// lower-case name without the prefix (first value only). An empty name (the
// header is exactly the prefix) returns InvalidArgument, and so does a name
// given twice under different letter cases. The total size is checked with
// object.ValidateUserMetadata (MetadataTooLarge). It returns nil when there
// are none.
func ParseUserMetadata(h http.Header) (map[string]string, error) {
	var md map[string]string
	for key, vals := range h {
		if !hasPrefixFold(key, userMetadataPrefix) {
			continue
		}
		name := strings.ToLower(key[len(userMetadataPrefix):])
		if name == "" {
			return nil, s3err.Errorf(s3err.InvalidArgument, "user metadata name must not be empty")
		}
		if md == nil {
			md = make(map[string]string)
		}
		if _, dup := md[name]; dup {
			return nil, s3err.Errorf(s3err.InvalidArgument, "user metadata name is repeated")
		}
		var value string
		if len(vals) > 0 {
			value = vals[0]
		}
		if containsControl(value) {
			return nil, s3err.Errorf(s3err.InvalidArgument, "user metadata value contains a control character")
		}
		md[name] = value
	}
	if err := object.ValidateUserMetadata(md); err != nil {
		return nil, err
	}
	return md, nil
}
