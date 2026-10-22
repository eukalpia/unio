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
// bytes, never clocks or random sources. Times and random version tails are
// chosen by the proposer and carried in the command.
package fsm
