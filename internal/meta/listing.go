package meta

// ListResult is a page of stored object records.
type ListResult struct {
	Objects        []ObjectRecord
	CommonPrefixes []string
	IsTruncated    bool
	NextAfter      string
}
