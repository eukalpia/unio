package object

// ListOptions configures object listing.
type ListOptions struct {
	Prefix    string
	Delimiter string
	// After is the exclusive start key: listing returns keys (or common
	// prefixes) strictly greater than After.
	After string
	// MaxKeys is the page size, 1..MaxListKeys. Zero means MaxListKeys.
	MaxKeys int
}

// ListResult is one page of a listing.
type ListResult struct {
	Objects        []Info
	CommonPrefixes []string
	IsTruncated    bool
	// NextAfter is the value to pass as ListOptions.After for the next page.
	// It is the last key or common prefix returned.
	NextAfter string
}
