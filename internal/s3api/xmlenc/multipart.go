package xmlenc

// AppendInitiateMultipartUpload appends an InitiateMultipartUpload response to
// dst and returns the extended slice. A nil r encodes as the zero value.
func AppendInitiateMultipartUpload(dst []byte, r *InitiateMultipartUploadResult) []byte {
	if r == nil {
		r = &InitiateMultipartUploadResult{}
	}
	dst = appendRoot(dst, "InitiateMultipartUploadResult")
	dst = appendTextElem(dst, "Bucket", r.Bucket)
	dst = appendTextElem(dst, "Key", r.Key)
	dst = appendTextElem(dst, "UploadId", r.UploadID)
	return appendClose(dst, "InitiateMultipartUploadResult")
}

// AppendCompleteMultipartUpload appends a CompleteMultipartUpload response to
// dst and returns the extended slice. Checksum elements and ChecksumType are
// emitted only when non-empty. A nil r encodes as the zero value.
func AppendCompleteMultipartUpload(dst []byte, r *CompleteMultipartUploadResult) []byte {
	if r == nil {
		r = &CompleteMultipartUploadResult{}
	}
	dst = appendRoot(dst, "CompleteMultipartUploadResult")
	dst = appendTextElem(dst, "Location", r.Location)
	dst = appendTextElem(dst, "Bucket", r.Bucket)
	dst = appendTextElem(dst, "Key", r.Key)
	dst = appendTextElem(dst, "ETag", r.ETag)
	dst = r.appendChecksums(dst)
	if r.ChecksumType != "" {
		dst = appendTextElem(dst, "ChecksumType", r.ChecksumType)
	}
	return appendClose(dst, "CompleteMultipartUploadResult")
}

// AppendListParts appends a ListParts response to dst and returns the extended
// slice. Initiator and Owner are emitted only when r.Owner is non-nil. A nil r
// encodes as the zero value.
func AppendListParts(dst []byte, r *ListPartsResult) []byte {
	if r == nil {
		r = &ListPartsResult{}
	}
	dst = appendRoot(dst, "ListPartsResult")
	dst = appendTextElem(dst, "Bucket", r.Bucket)
	dst = appendTextElem(dst, "Key", r.Key)
	dst = appendTextElem(dst, "UploadId", r.UploadID)
	dst = appendInitiatorAndOwner(dst, r.Owner)
	dst = appendTextElem(dst, "StorageClass", r.StorageClass)
	dst = appendIntElem(dst, "PartNumberMarker", int64(r.PartNumberMarker))
	dst = appendIntElem(dst, "NextPartNumberMarker", int64(r.NextPartNumberMarker))
	dst = appendIntElem(dst, "MaxParts", int64(r.MaxParts))
	dst = appendBoolElem(dst, "IsTruncated", r.IsTruncated)
	for i := range r.Parts {
		dst = appendPart(dst, &r.Parts[i])
	}
	dst = appendTextElem(dst, "ChecksumAlgorithm", r.ChecksumAlgorithm)
	return appendClose(dst, "ListPartsResult")
}

// appendPart appends one Part entry. Non-empty checksum elements are emitted.
func appendPart(dst []byte, p *Part) []byte {
	dst = appendOpen(dst, "Part")
	dst = appendIntElem(dst, "PartNumber", int64(p.PartNumber))
	dst = appendTimeElem(dst, "LastModified", p.LastModified)
	dst = appendTextElem(dst, "ETag", p.ETag)
	dst = appendIntElem(dst, "Size", p.Size)
	dst = p.appendChecksums(dst)
	return appendClose(dst, "Part")
}

// AppendListMultipartUploads appends a ListMultipartUploads response to dst and
// returns the extended slice. Key-like values follow EncodingType. A nil r
// encodes as the zero value.
func AppendListMultipartUploads(dst []byte, r *ListMultipartUploadsResult) []byte {
	if r == nil {
		r = &ListMultipartUploadsResult{}
	}
	urlEnc := r.EncodingType == encodingURL
	dst = appendRoot(dst, "ListMultipartUploadsResult")
	dst = appendTextElem(dst, "Bucket", r.Bucket)
	dst = appendKeyElem(dst, "KeyMarker", r.KeyMarker, urlEnc)
	dst = appendTextElem(dst, "UploadIdMarker", r.UploadIDMarker)
	dst = appendKeyElem(dst, "NextKeyMarker", r.NextKeyMarker, urlEnc)
	dst = appendKeyElem(dst, "Prefix", r.Prefix, urlEnc)
	dst = appendKeyElem(dst, "Delimiter", r.Delimiter, urlEnc)
	dst = appendTextElem(dst, "NextUploadIdMarker", r.NextUploadIDMarker)
	dst = appendTextElem(dst, "EncodingType", r.EncodingType)
	dst = appendIntElem(dst, "MaxUploads", int64(r.MaxUploads))
	dst = appendBoolElem(dst, "IsTruncated", r.IsTruncated)
	for i := range r.Uploads {
		dst = appendUpload(dst, &r.Uploads[i], urlEnc)
	}
	for _, p := range r.CommonPrefixes {
		dst = appendCommonPrefix(dst, p, urlEnc)
	}
	return appendClose(dst, "ListMultipartUploadsResult")
}

// appendUpload appends one Upload entry. Initiator and Owner are emitted only
// when u.Owner is non-nil.
func appendUpload(dst []byte, u *Upload, urlEnc bool) []byte {
	dst = appendOpen(dst, "Upload")
	dst = appendKeyElem(dst, "Key", u.Key, urlEnc)
	dst = appendTextElem(dst, "UploadId", u.UploadID)
	dst = appendInitiatorAndOwner(dst, u.Owner)
	dst = appendTextElem(dst, "StorageClass", u.StorageClass)
	dst = appendTimeElem(dst, "Initiated", u.Initiated)
	return appendClose(dst, "Upload")
}
