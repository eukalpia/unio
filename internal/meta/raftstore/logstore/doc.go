// Package logstore stores the Raft logs and stable state of every Raft group
// on one node in a single Pebble database.
//
// Layout. Every key starts with the group id as a 4-byte big-endian integer,
// so the keys of one group form one contiguous range that no other group's
// keys fall into. The fifth byte is a tag:
//
//	group(4) 'L' index(8)  -> encoded raft.Log   raft.LogStore entries
//	group(4) 'S' key bytes -> raw value          raft.StableStore entries
//
// Durability. Every write is committed with pebble.Sync before its call
// returns. StoreLogs commits all entries of one call in one batch, so a call
// is either fully stored or not stored at all.
//
// Dense indices. StoreLogs only accepts entries that continue the group's
// log: when the group has logs, the first new index must be the last index
// plus one; entries of one call must be consecutive; index 0 is rejected
// because FirstIndex and LastIndex report 0 for an empty group. A group with
// no logs accepts any non-zero starting index, which is what Raft needs after
// a snapshot has removed all logs. DeleteRange may remove any inclusive range.
// Raft only removes prefixes and suffixes; a range strictly inside the log
// leaves a hole that GetLog reports as raft.ErrLogNotFound.
//
// GroupStore implements raft.MonotonicLogStore and reports true. The reason is
// the dense-index rule above: appended indices never leave gaps, so Raft may
// rely on that after a snapshot restore by removing every old log (see
// raft.MonotonicLogStore) instead of using a gap to find the restore point.
//
// Concurrency. Different groups write concurrently, and Pebble groups their
// fsyncs. Writes to one group are serialized so that its cached bounds always
// match the database. FirstIndex and LastIndex never wait for a write.
package logstore
