package engine

import (
	"github.com/eukalpia/unio/internal/object"
	"github.com/eukalpia/unio/internal/s3err"
)

// deleteMarkerCause is the cause of the NoSuchKey error that a read returns
// when the latest version of a key is a delete marker. The front end needs
// the marker's attributes to set x-amz-delete-marker and x-amz-version-id, so
// the cause exposes them through DeleteMarkerInfo.
//
// The error itself is a *s3err.Error with code NoSuchKey. A front end that
// only needs the code can ignore the cause. One that needs the marker
// declares a local interface and calls errors.As:
//
//	var dm interface{ DeleteMarkerInfo() object.Info }
//	if errors.As(err, &dm) { info := dm.DeleteMarkerInfo() ... }
type deleteMarkerCause struct {
	info object.Info
}

// Error implements error.
func (c deleteMarkerCause) Error() string {
	return "latest version is a delete marker"
}

// DeleteMarkerInfo returns the description of the delete marker.
func (c deleteMarkerCause) DeleteMarkerInfo() object.Info {
	return c.info
}

// noSuchKeyForMarker returns NoSuchKey for a key whose latest version is the
// delete marker described by info.
func noSuchKeyForMarker(info object.Info) error {
	return s3err.Wrap(s3err.NoSuchKey, deleteMarkerCause{info: info})
}
