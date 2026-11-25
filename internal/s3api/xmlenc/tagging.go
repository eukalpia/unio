package xmlenc

import "github.com/eukalpia/unio/internal/object"

// AppendTagging appends a GetObjectTagging response to dst and returns the
// extended slice. The Tag elements keep the order of tags, and an empty set
// encodes an empty TagSet element.
func AppendTagging(dst []byte, tags object.TagSet) []byte {
	dst = appendRoot(dst, "Tagging")
	dst = appendOpen(dst, "TagSet")
	for i := range tags {
		dst = appendOpen(dst, "Tag")
		dst = appendTextElem(dst, "Key", tags[i].Key)
		dst = appendTextElem(dst, "Value", tags[i].Value)
		dst = appendClose(dst, "Tag")
	}
	dst = appendClose(dst, "TagSet")
	return appendClose(dst, "Tagging")
}
