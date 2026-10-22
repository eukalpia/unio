package xmlenc

// AppendError appends an S3 error document to dst and returns the extended
// slice. The Error root has no namespace. A nil e encodes as the zero value.
func AppendError(dst []byte, e *ErrorResponse) []byte {
	if e == nil {
		e = &ErrorResponse{}
	}
	dst = appendDecl(dst)
	dst = appendOpen(dst, "Error")
	dst = appendTextElem(dst, "Code", e.Code)
	dst = appendTextElem(dst, "Message", e.Message)
	dst = appendTextElem(dst, "Resource", e.Resource)
	dst = appendTextElem(dst, "RequestId", e.RequestID)
	dst = appendTextElem(dst, "HostId", e.HostID)
	return appendClose(dst, "Error")
}
