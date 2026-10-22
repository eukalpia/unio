package metatest

import (
	"github.com/eukalpia/unio/internal/erasure"
	"github.com/eukalpia/unio/internal/ids"
	"github.com/eukalpia/unio/internal/meta"
	"github.com/eukalpia/unio/internal/object"
)

// The clone helpers deep-copy records so that no slice or map is shared
// between the Fake and its callers. They preserve nil versus empty, because
// the meta contract gives Inline and Parts meaning through nil-ness.

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append(make([]byte, 0, len(b)), b...)
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneChecksum(c object.Checksum) object.Checksum {
	c.Value = cloneBytes(c.Value)
	return c
}

func cloneLayout(l erasure.Layout) erasure.Layout {
	if l.Drives != nil {
		l.Drives = append(make([]ids.DriveID, 0, len(l.Drives)), l.Drives...)
	}
	return l
}

func clonePart(p meta.PartRecord) meta.PartRecord {
	p.Checksum = cloneChecksum(p.Checksum)
	p.Layout = cloneLayout(p.Layout)
	return p
}

func clonePartSlice(ps []meta.PartRecord) []meta.PartRecord {
	if ps == nil {
		return nil
	}
	out := make([]meta.PartRecord, len(ps))
	for i := range ps {
		out[i] = clonePart(ps[i])
	}
	return out
}

func cloneObject(r meta.ObjectRecord) meta.ObjectRecord {
	r.Inline = cloneBytes(r.Inline)
	r.UserMetadata = cloneMap(r.UserMetadata)
	r.Checksum = cloneChecksum(r.Checksum)
	r.Parts = clonePartSlice(r.Parts)
	return r
}

func cloneUpload(u meta.UploadRecord) meta.UploadRecord {
	u.Options.UserMetadata = cloneMap(u.Options.UserMetadata)
	return u
}
