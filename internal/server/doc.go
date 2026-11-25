// Package server assembles a Unio node from the modules and runs it.
//
// It is the only package that imports implementations; every other package
// depends on contracts (ADR-0007). Start brings the node up in dependency
// order:
//
//  1. cluster identity: the cluster id is derived from the cluster secret,
//     the cluster map from the endpoints, and the map is reconciled with the
//     one stored on this node, so a changed layout is refused instead of
//     silently forming a different cluster;
//  2. internode transport: node certificates from the cluster CA, and UniWire
//     with mutual TLS;
//  3. local drives, each formatted with, or checked against, the drive id
//     derived for its slot;
//  4. cluster services: the Raft network, peer health, remote drive access,
//     and the internode listener;
//  5. the metadata store;
//  6. the data path: erasure-set placement, background healing and the
//     object engine;
//  7. identity and access management, and the S3 front end.
//
// Every step registers how to undo itself, and Close runs those steps in
// reverse, so a start that fails half way releases everything it acquired,
// and a stop drains requests before the metadata store hands over its Raft
// leaderships and the drives close.
package server
