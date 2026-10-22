// Package meta defines the metadata plane: stored object, part and upload
// records, the linearizable Store for buckets, versions and multipart
// state, and the SystemKV used for IAM and cluster state.
//
// Package meta/raftstore implements Store and SystemKV on Multi-Raft
// partitions backed by Pebble. Package meta/recordpb converts records to
// and from their stored protobuf form. Package meta/metatest provides a
// fake and the conformance suites.
package meta
