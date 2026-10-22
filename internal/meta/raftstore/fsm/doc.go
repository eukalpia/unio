// Package fsm implements the Raft state machines of the metadata plane on
// Pebble.
//
// Two kinds of machine share one implementation. The system group (group 0)
// stores buckets and system key-value entries. Each data partition stores
// object versions, multipart uploads and parts, and a per-bucket control
// record (fence and versioning) that lets data commands check bucket state
// in the same log as the data.
//
// Every command is applied in one Pebble batch together with the index of
// the command, so the database always reflects a prefix of the Raft log.
// Commands are deterministic: they read only the database and the command
// bytes, never clocks or random sources. Times and version ids are chosen by
// the proposer and carried in the command.
//
// Validation boundary. Every replica applies the same log, and the log may
// contain commands that a peer proposed. Each handler therefore validates its
// decoded command before it reads or writes state: missing sub-messages,
// empty or oversized keys and ids, invalid bucket names, part numbers outside
// the S3 range, erasure layouts whose shard counts disagree with their drives
// or fall below the write quorum, inline data above the threshold, and system
// values above the store limit are all rejected. A rejected command returns a
// deterministic error Outcome, leaves the visible state unchanged, and still
// advances the applied index, so every replica takes the same branch.
//
// Apply never panics on any input. It decodes and applies a command, or rejects
// it, for every byte sequence. Read never panics either. A panic that a handler
// still raises is a bug; it is recovered, counted (see PanicCount), and rejected
// the same way on every replica, so a single bad entry cannot split the cluster.
// FuzzApply asserts that PanicCount stays zero and that rejected commands leave
// the state unchanged.
//
// Stored records are encoded deterministically (map keys sorted), so two
// replicas that applied the same log hold byte-identical databases.
package fsm
