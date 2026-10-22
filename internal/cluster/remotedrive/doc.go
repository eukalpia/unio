// Package remotedrive serves local drives to other nodes and provides a
// drive.Drive client for drives hosted elsewhere.
//
// Server registers drive methods on a UniWire transport: shard writes and reads
// are streams, and stat, delete, info and paginated walks are RPCs. Client
// implements drive.Drive over those methods. Transport failures are reported
// as drive.ErrOffline, and drive errors keep their sentinels across the wire.
package remotedrive
