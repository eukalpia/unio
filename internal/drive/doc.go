// Package drive defines the contract of a storage drive: immutable shards
// addressed by ids.ShardID.
//
// Implementations:
//   - drive/local: the on-disk engine (packed segments, blob files, index).
//   - cluster/remotedrive: a drive on another node reached over UniWire.
//
// Package drive/drivetest provides an in-memory drive and the conformance
// suite every implementation must pass.
package drive
