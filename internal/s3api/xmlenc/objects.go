package xmlenc

// AppendCopyObject appends a CopyObject response to dst and returns the
// extended slice. Checksum elements and ChecksumType are emitted only when
// non-empty. A nil r encodes as the zero value.
func AppendCopyObject(dst []byte, r *CopyObjectResult) []byte {
	if r == nil {
		r = &CopyObjectResult{}
	}
	dst = appendRoot(dst, "CopyObjectResult")
	dst = appendTextElem(dst, "ETag", r.ETag)
	dst = appendTimeElem(dst, "LastModified", r.LastModified)
	dst = r.appendChecksums(dst)
	if r.ChecksumType != "" {
		dst = appendTextElem(dst, "ChecksumType", r.ChecksumType)
	}
	return appendClose(dst, "CopyObjectResult")
}

// AppendDeleteResult appends a DeleteObjects response to dst and returns the
// extended slice. A nil r encodes as the zero value.
//
// The result has no encoding-type field, so keys are always escaped as text.
func AppendDeleteResult(dst []byte, r *DeleteResult) []byte {
	if r == nil {
		r = &DeleteResult{}
	}
	dst = appendRoot(dst, "DeleteResult")
	for i := range r.Deleted {
		dst = appendDeleted(dst, &r.Deleted[i])
	}
	for i := range r.Errors {
		dst = appendDeleteError(dst, &r.Errors[i])
	}
	return appendClose(dst, "DeleteResult")
}

// appendDeleted appends one Deleted entry. DeleteMarker and
// DeleteMarkerVersionId are emitted only when d.DeleteMarker is true.
func appendDeleted(dst []byte, d *DeletedObject) []byte {
	dst = appendOpen(dst, "Deleted")
	dst = appendTextElem(dst, "Key", d.Key)
	if d.VersionID != "" {
		dst = appendTextElem(dst, "VersionId", d.VersionID)
	}
	if d.DeleteMarker {
		dst = appendBoolElem(dst, "DeleteMarker", true)
		dst = appendTextElem(dst, "DeleteMarkerVersionId", d.DeleteMarkerVersionID)
	}
	return appendClose(dst, "Deleted")
}

// appendDeleteError appends one Error entry of a DeleteResult.
func appendDeleteError(dst []byte, e *DeleteError) []byte {
	dst = appendOpen(dst, "Error")
	dst = appendTextElem(dst, "Key", e.Key)
	if e.VersionID != "" {
		dst = appendTextElem(dst, "VersionId", e.VersionID)
	}
	dst = appendTextElem(dst, "Code", e.Code)
	dst = appendTextElem(dst, "Message", e.Message)
	return appendClose(dst, "Error")
}
