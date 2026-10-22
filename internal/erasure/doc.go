// Package erasure defines erasure-coded storage across a fixed group of
// drives: the recorded Layout of written shards, the Set interface that
// writes and reads data with quorums and bitrot protection, and the
// Registry that places new data.
//
// Package erasure/rs implements Set with Reed-Solomon coding; package
// erasure/erasuretest provides a fake and the conformance suite.
package erasure
