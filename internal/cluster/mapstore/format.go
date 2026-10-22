package mapstore

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"

	"github.com/eukalpia/unio/internal/cluster"
	"github.com/eukalpia/unio/internal/cluster/mappb"
)

// The file layout is fixed for format version 1:
//
//	offset  size  field
//	0       8     magic "UNIOCMAP"
//	8       1     format version, 1
//	9       4     CRC-32C of the payload, big endian
//	13      n     payload: mappb.Marshal of the cluster map
const (
	magic         = "UNIOCMAP"
	formatVersion = 1
	headerLen     = len(magic) + 1 + 4

	// maxFileSize bounds the file that Load reads. A cluster map is a few
	// kilobytes per node and drive, so the bound leaves room for very large
	// clusters while keeping a corrupt or hostile file from exhausting memory.
	maxFileSize = 16 << 20

	// maxPayload is the largest payload that fits in a file of maxFileSize.
	maxPayload = maxFileSize - headerLen
)

// castagnoli is the CRC-32C table, the same checksum the drives use.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// encode returns the file bytes that store m. It fails if the encoded map
// would be too large for Load to accept, so that Save never writes a file
// that the next start cannot read.
func encode(m *cluster.Map) ([]byte, error) {
	payload, err := mappb.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("mapstore: encode cluster map: %w", err)
	}
	if len(payload) > maxPayload {
		return nil, fmt.Errorf("mapstore: cluster map is %d bytes, the limit is %d", len(payload), maxPayload)
	}
	b := make([]byte, 0, headerLen+len(payload))
	b = append(b, magic...)
	b = append(b, formatVersion)
	b = binary.BigEndian.AppendUint32(b, crc32.Checksum(payload, castagnoli))
	return append(b, payload...), nil
}

// decode parses the bytes of a cluster map file. Every failure wraps
// ErrCorrupt. decode never panics, whatever b holds, and it reads nothing
// beyond b.
func decode(b []byte) (*cluster.Map, error) {
	if len(b) < headerLen {
		return nil, corruptf("file is %d bytes, shorter than the %d-byte header", len(b), headerLen)
	}
	if string(b[:len(magic)]) != magic {
		return nil, corruptf("bad magic, so this is not a cluster map file")
	}
	if v := b[len(magic)]; v != formatVersion {
		return nil, corruptf("format version %d is not supported, this build reads version %d", v, formatVersion)
	}
	stored := binary.BigEndian.Uint32(b[len(magic)+1 : headerLen])
	payload := b[headerLen:]
	if sum := crc32.Checksum(payload, castagnoli); sum != stored {
		return nil, corruptf("checksum mismatch: stored %08x, computed %08x", stored, sum)
	}
	m, err := mappb.Unmarshal(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: decode payload: %w", ErrCorrupt, err)
	}
	return m, nil
}

// corruptf returns an error that wraps ErrCorrupt and describes the damage.
func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}
