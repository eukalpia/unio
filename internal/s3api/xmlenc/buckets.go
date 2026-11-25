package xmlenc

// AppendListBuckets appends a ListAllMyBuckets response to dst and returns the
// extended slice. A nil r encodes as the zero value.
func AppendListBuckets(dst []byte, r *ListBucketsResult) []byte {
	if r == nil {
		r = &ListBucketsResult{}
	}
	dst = appendRoot(dst, "ListAllMyBucketsResult")
	dst = appendOwnerElem(dst, "Owner", &r.Owner)
	dst = appendOpen(dst, "Buckets")
	for i := range r.Buckets {
		b := &r.Buckets[i]
		dst = appendOpen(dst, "Bucket")
		dst = appendTextElem(dst, "Name", b.Name)
		dst = appendTimeElem(dst, "CreationDate", b.CreationDate)
		dst = appendClose(dst, "Bucket")
	}
	dst = appendClose(dst, "Buckets")
	if r.ContinuationToken != "" {
		dst = appendTextElem(dst, "ContinuationToken", r.ContinuationToken)
	}
	if r.Prefix != "" {
		dst = appendTextElem(dst, "Prefix", r.Prefix)
	}
	return appendClose(dst, "ListAllMyBucketsResult")
}

// AppendLocationConstraint appends a GetBucketLocation response for region to
// dst and returns the extended slice. An empty region yields an empty element.
func AppendLocationConstraint(dst []byte, region string) []byte {
	dst = appendRoot(dst, "LocationConstraint")
	dst = AppendEscape(dst, region)
	return appendClose(dst, "LocationConstraint")
}
