package core

// Protocol and safety limits. These are part of the contract: every layer
// may rely on inputs that reached it having been checked against them.
const (
	// MaxObjectKeyLength is the maximum object key length in bytes.
	MaxObjectKeyLength = 1024
	// MaxUserMetadataSize is the maximum total size of user metadata
	// (keys plus values) in bytes.
	MaxUserMetadataSize = 2 << 10
	// MaxHeaderBytes is the maximum size of a request header section.
	MaxHeaderBytes = 64 << 10
	// MaxXMLBodySize is the maximum size of an XML request body.
	MaxXMLBodySize = 2 << 20
	// MaxObjectSize is the maximum size of a single object.
	MaxObjectSize = 5 << 40
	// MaxPutObjectSize is the maximum size of a single PUT (not multipart).
	MaxPutObjectSize = 5 << 30
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
	// MaxObjectTags is the maximum number of tags on an object.
	MaxObjectTags = 10
)

// Storage tier defaults (see ADR-0005).
const (
	// DefaultInlineThreshold is the largest object stored inline in metadata.
	DefaultInlineThreshold = 4 << 10
	// MaxInlineThreshold bounds the configurable inline threshold.
	MaxInlineThreshold = 64 << 10
	// DefaultPackedThreshold is the largest object whose shards are packed
	// into segments. Larger objects and all multipart parts use blob files.
	DefaultPackedThreshold = 1 << 20
	// DefaultStripeSize is the amount of object data per erasure stripe.
	DefaultStripeSize = 1 << 20
)
