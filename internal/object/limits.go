package object

import "unicode/utf8"

// Protocol and safety limits. Every layer may rely on inputs that reached
// it having been checked against them.
const (
	// MaxKeyLength is the maximum object key length in bytes.
	MaxKeyLength = 1024
	// MaxUserMetadataSize is the maximum total size of user metadata
	// (keys plus values) in bytes.
	MaxUserMetadataSize = 2 << 10
	// MaxHeaderBytes is the maximum size of a request header section.
	MaxHeaderBytes = 64 << 10
	// MaxXMLBodySize is the maximum size of an XML request body.
	MaxXMLBodySize = 2 << 20
	// MaxObjectSize is the maximum size of a single object.
	MaxObjectSize = 5 << 40
	// MaxPutSize is the maximum size of a single PUT (not multipart).
	MaxPutSize = 5 << 30
	// MaxPartSize is the maximum size of one multipart part.
	MaxPartSize = 5 << 30
	// MinPartSize is the minimum size of every multipart part except the last.
	MinPartSize = 5 << 20
	// MaxPartNumber is the highest allowed multipart part number.
	MaxPartNumber = 10000
	// MaxDeleteObjects is the maximum number of keys in one DeleteObjects call.
	MaxDeleteObjects = 1000
	// MaxListKeys is the maximum page size of listing operations.
	MaxListKeys = 1000
	// MaxTags is the maximum number of tags on an object.
	MaxTags = 10
	// MaxTagKeyLength is the maximum length of a tag key in characters.
	MaxTagKeyLength = 128
	// MaxTagValueLength is the maximum length of a tag value in characters.
	MaxTagValueLength = 256
	// MaxEncodedTagsLength bounds the URL-encoded form of a tag set. A
	// character takes at most utf8.UTFMax bytes, and percent-encoding turns
	// each byte into at most three, so this covers every valid tag set.
	MaxEncodedTagsLength = MaxTags * (3*utf8.UTFMax*(MaxTagKeyLength+MaxTagValueLength) + len("=&"))
)

// Storage tier defaults (ADR-0005).
const (
	// DefaultInlineThreshold is the largest object stored inline in metadata.
	DefaultInlineThreshold = 4 << 10
	// MaxInlineThreshold bounds the configurable inline threshold.
	MaxInlineThreshold = 64 << 10
	// DefaultPackedThreshold is the largest object whose shards are packed
	// into segments. Larger objects and all multipart parts use blob files.
	DefaultPackedThreshold = 1 << 20
)
