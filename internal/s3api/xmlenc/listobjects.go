package xmlenc

// AppendListObjectsV2 appends a ListObjectsV2 response to dst and returns the
// extended slice. A nil r encodes as the zero value.
func AppendListObjectsV2(dst []byte, r *ListObjectsV2Result) []byte {
	if r == nil {
		r = &ListObjectsV2Result{}
	}
	urlEnc := r.EncodingType == encodingURL
	dst = appendRoot(dst, "ListBucketResult")
	dst = appendTextElem(dst, "Name", r.Name)
	dst = appendKeyElem(dst, "Prefix", r.Prefix, urlEnc)
	if r.Delimiter != "" {
		dst = appendKeyElem(dst, "Delimiter", r.Delimiter, urlEnc)
	}
	if r.StartAfter != "" {
		dst = appendKeyElem(dst, "StartAfter", r.StartAfter, urlEnc)
	}
	// Continuation tokens are opaque and never percent-encoded.
	if r.ContinuationToken != "" {
		dst = appendTextElem(dst, "ContinuationToken", r.ContinuationToken)
	}
	if r.NextContinuationToken != "" {
		dst = appendTextElem(dst, "NextContinuationToken", r.NextContinuationToken)
	}
	if r.EncodingType != "" {
		dst = appendTextElem(dst, "EncodingType", r.EncodingType)
	}
	dst = appendIntElem(dst, "KeyCount", int64(r.KeyCount))
	dst = appendIntElem(dst, "MaxKeys", int64(r.MaxKeys))
	dst = appendBoolElem(dst, "IsTruncated", r.IsTruncated)
	for i := range r.Contents {
		dst = appendContent(dst, &r.Contents[i], urlEnc)
	}
	for _, p := range r.CommonPrefixes {
		dst = appendCommonPrefix(dst, p, urlEnc)
	}
	return appendClose(dst, "ListBucketResult")
}

// AppendListObjectsV1 appends a ListObjects (V1) response to dst and returns
// the extended slice. A nil r encodes as the zero value.
func AppendListObjectsV1(dst []byte, r *ListObjectsV1Result) []byte {
	if r == nil {
		r = &ListObjectsV1Result{}
	}
	urlEnc := r.EncodingType == encodingURL
	dst = appendRoot(dst, "ListBucketResult")
	dst = appendTextElem(dst, "Name", r.Name)
	dst = appendKeyElem(dst, "Prefix", r.Prefix, urlEnc)
	dst = appendKeyElem(dst, "Marker", r.Marker, urlEnc)
	if r.Delimiter != "" {
		dst = appendKeyElem(dst, "Delimiter", r.Delimiter, urlEnc)
	}
	if r.NextMarker != "" {
		dst = appendKeyElem(dst, "NextMarker", r.NextMarker, urlEnc)
	}
	if r.EncodingType != "" {
		dst = appendTextElem(dst, "EncodingType", r.EncodingType)
	}
	dst = appendIntElem(dst, "MaxKeys", int64(r.MaxKeys))
	dst = appendBoolElem(dst, "IsTruncated", r.IsTruncated)
	for i := range r.Contents {
		dst = appendContent(dst, &r.Contents[i], urlEnc)
	}
	for _, p := range r.CommonPrefixes {
		dst = appendCommonPrefix(dst, p, urlEnc)
	}
	return appendClose(dst, "ListBucketResult")
}

// appendContent appends one Contents entry. The Owner element is emitted only
// when c.Owner is non-nil.
func appendContent(dst []byte, c *Content, urlEnc bool) []byte {
	dst = appendOpen(dst, "Contents")
	dst = appendKeyElem(dst, "Key", c.Key, urlEnc)
	dst = appendTimeElem(dst, "LastModified", c.LastModified)
	dst = appendTextElem(dst, "ETag", c.ETag)
	if c.ChecksumAlgorithm != "" {
		dst = appendTextElem(dst, "ChecksumAlgorithm", c.ChecksumAlgorithm)
	}
	dst = appendIntElem(dst, "Size", c.Size)
	dst = appendTextElem(dst, "StorageClass", c.StorageClass)
	if c.Owner != nil {
		dst = appendOwnerElem(dst, "Owner", c.Owner)
	}
	return appendClose(dst, "Contents")
}

// appendCommonPrefix appends one CommonPrefixes entry.
func appendCommonPrefix(dst []byte, prefix string, urlEnc bool) []byte {
	dst = appendOpen(dst, "CommonPrefixes")
	dst = appendKeyElem(dst, "Prefix", prefix, urlEnc)
	return appendClose(dst, "CommonPrefixes")
}
