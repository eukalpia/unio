package raftnet

//lint:file-ignore SA1019 bindSender takes the legacy Leader and Candidate fields that raft 1.8 still reads when Addr is empty.

import (
	"bytes"

	"github.com/hashicorp/raft"

	"github.com/eukalpia/unio/internal/ids"
)

// bindSender ties the identity fields of a decoded request to the sender that
// the transport authenticated. raft trusts those fields. It reads RPCHeader.Addr
// when the address is set, and it falls back to the legacy Leader or Candidate
// field when it is not. So every identity field must name the sender.
//
// The header ID must equal the sender. Each address or legacy field must
// either name the sender or be empty. A field that names another node is a
// spoofing attempt, and the request is refused with senderMismatch. An empty
// field is filled with the sender's identity, which is what raft itself would
// have sent. After a successful bind, every identity field raft reads names the
// sender exactly.
func bindSender(from ids.NodeID, h *raft.RPCHeader, legacy ...*[]byte) error {
	if from == "" {
		return senderMismatch()
	}
	want := []byte(from)
	if !bytes.Equal(h.ID, want) {
		return senderMismatch()
	}
	fields := append([]*[]byte{&h.Addr}, legacy...)
	for _, f := range fields {
		switch {
		case len(*f) == 0:
			*f = append([]byte(nil), want...)
		case !bytes.Equal(*f, want):
			return senderMismatch()
		}
	}
	return nil
}
