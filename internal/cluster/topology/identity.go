package topology

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"

	"github.com/eukalpia/unio/internal/cluster/pki"
	"github.com/eukalpia/unio/internal/ids"
)

const (
	// clusterIDInfo is the HKDF label of the cluster id. The label is part of
	// the v1 format: changing it gives every cluster a new id.
	clusterIDInfo = "unio/v1/cluster-id"

	// driveIDDomain is the domain prefix of the drive id digest. It is part of
	// the v1 format: changing it renames every drive.
	driveIDDomain = "unio/v1/drive-id"

	// clusterIDLen is the length of the cluster id before hex encoding.
	clusterIDLen = 16
)

// DeriveClusterID returns the id of the cluster that secret belongs to. The id
// is 16 bytes of HKDF-SHA256 output with the label "unio/v1/cluster-id", as 32
// lowercase hex digits. Nodes that hold the same secret derive the same id
// without talking to each other, which is what lets them agree on a cluster
// map. The secret must be at least pki.MinSecretLen bytes.
func DeriveClusterID(secret []byte) (string, error) {
	if len(secret) < pki.MinSecretLen {
		return "", fmt.Errorf("topology: cluster secret must be at least %d bytes, got %d", pki.MinSecretLen, len(secret))
	}
	raw, err := hkdf.Key(sha256.New, secret, nil, clusterIDInfo, clusterIDLen)
	if err != nil {
		return "", fmt.Errorf("topology: derive cluster id: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// DriveIDFor returns the id of the drive at path on node in the cluster
// clusterID. The id is the first 16 bytes of
//
//	SHA-256("unio/v1/drive-id" 0x00 clusterID 0x00 node 0x00 filepath.Clean(path))
//
// Every node derives the same id for the same drive, and a formatted drive
// keeps its id across restarts because the id depends only on the
// configuration. Paths that differ only in redundant separators or a trailing
// slash give the same id. The id is never zero. The inputs are not validated;
// callers pass a cluster id from DeriveClusterID and a node id that has passed
// pki.ValidateNodeID.
func DriveIDFor(clusterID string, node ids.NodeID, path string) ids.DriveID {
	in := make([]byte, 0, len(driveIDDomain)+len(clusterID)+len(node)+len(path)+3)
	in = append(in, driveIDDomain...)
	in = append(in, 0)
	in = append(in, clusterID...)
	in = append(in, 0)
	in = append(in, node...)
	in = append(in, 0)
	in = append(in, filepath.Clean(path)...)
	return driveIDFromDigest(sha256.Sum256(in))
}

// driveIDFromDigest returns the first 16 bytes of sum as a drive id. A zero
// prefix occurs with probability 2^-128. It is replaced by the digest of the
// digest, so the result is never zero and is still a function of the input.
func driveIDFromDigest(sum [sha256.Size]byte) ids.DriveID {
	var id ids.DriveID
	for {
		copy(id[:], sum[:])
		if !id.IsZero() {
			return id
		}
		sum = sha256.Sum256(sum[:])
	}
}
