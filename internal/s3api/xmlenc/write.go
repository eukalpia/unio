package xmlenc

import (
	"strconv"
	"time"
)

const (
	// xmlDecl is the document declaration that starts every response.
	xmlDecl = `<?xml version="1.0" encoding="UTF-8"?>`
	// s3Xmlns is the namespace attribute carried by success roots.
	s3Xmlns = ` xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`
	// timeLayout is the S3 timestamp format. It is always UTC.
	timeLayout = "2006-01-02T15:04:05.000Z"
	// encodingURL is the EncodingType value that enables percent-encoding.
	encodingURL = "url"
)

// appendDecl appends the XML declaration.
func appendDecl(dst []byte) []byte {
	return append(dst, xmlDecl...)
}

// appendRoot appends the declaration and an opening success root element
// that carries the S3 namespace.
func appendRoot(dst []byte, name string) []byte {
	dst = appendDecl(dst)
	dst = append(dst, '<')
	dst = append(dst, name...)
	dst = append(dst, s3Xmlns...)
	return append(dst, '>')
}

// appendOpen appends an opening tag.
func appendOpen(dst []byte, name string) []byte {
	dst = append(dst, '<')
	dst = append(dst, name...)
	return append(dst, '>')
}

// appendClose appends a closing tag.
func appendClose(dst []byte, name string) []byte {
	dst = append(dst, '<', '/')
	dst = append(dst, name...)
	return append(dst, '>')
}

// appendTextElem appends an element whose text is escaped as XML text.
func appendTextElem(dst []byte, name, text string) []byte {
	dst = appendOpen(dst, name)
	dst = AppendEscape(dst, text)
	return appendClose(dst, name)
}

// appendKey appends a key-like value, percent-encoded when urlEnc is set.
func appendKey(dst []byte, key string, urlEnc bool) []byte {
	if urlEnc {
		return AppendPercent(dst, key)
	}
	return AppendEscape(dst, key)
}

// appendKeyElem appends an element whose key-like text follows urlEnc.
func appendKeyElem(dst []byte, name, key string, urlEnc bool) []byte {
	dst = appendOpen(dst, name)
	dst = appendKey(dst, key, urlEnc)
	return appendClose(dst, name)
}

// appendIntElem appends an element holding a decimal integer.
func appendIntElem(dst []byte, name string, v int64) []byte {
	dst = appendOpen(dst, name)
	dst = strconv.AppendInt(dst, v, 10)
	return appendClose(dst, name)
}

// appendBoolElem appends an element holding true or false.
func appendBoolElem(dst []byte, name string, v bool) []byte {
	dst = appendOpen(dst, name)
	dst = strconv.AppendBool(dst, v)
	return appendClose(dst, name)
}

// appendTimeElem appends an element holding t in S3 timestamp format.
func appendTimeElem(dst []byte, name string, t time.Time) []byte {
	dst = appendOpen(dst, name)
	dst = t.UTC().AppendFormat(dst, timeLayout)
	return appendClose(dst, name)
}

// appendOwnerElem appends an element holding an ID and DisplayName pair.
func appendOwnerElem(dst []byte, name string, o *Owner) []byte {
	dst = appendOpen(dst, name)
	dst = appendTextElem(dst, "ID", o.ID)
	dst = appendTextElem(dst, "DisplayName", o.DisplayName)
	return appendClose(dst, name)
}

// appendInitiatorAndOwner appends Initiator and Owner with the same value when
// o is non-nil.
func appendInitiatorAndOwner(dst []byte, o *Owner) []byte {
	if o == nil {
		return dst
	}
	dst = appendOwnerElem(dst, "Initiator", o)
	return appendOwnerElem(dst, "Owner", o)
}

// appendChecksums appends each checksum element that is non-empty.
func (c *Checksums) appendChecksums(dst []byte) []byte {
	if c.CRC32 != "" {
		dst = appendTextElem(dst, "ChecksumCRC32", c.CRC32)
	}
	if c.CRC32C != "" {
		dst = appendTextElem(dst, "ChecksumCRC32C", c.CRC32C)
	}
	if c.CRC64NVME != "" {
		dst = appendTextElem(dst, "ChecksumCRC64NVME", c.CRC64NVME)
	}
	if c.SHA1 != "" {
		dst = appendTextElem(dst, "ChecksumSHA1", c.SHA1)
	}
	if c.SHA256 != "" {
		dst = appendTextElem(dst, "ChecksumSHA256", c.SHA256)
	}
	return dst
}
