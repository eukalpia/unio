// Package chaos runs crash, partition and drive-fault scenarios against a
// local Unio cluster started with deploy/compose/cluster-4x4.yml.
//
// The package has two layers. The harness (Runner, Cluster, Network, Faults,
// Workload, Ledger, Verify) is pure Go and is covered by unit tests that need
// neither Docker nor a running cluster. The scenarios (TestDurability,
// TestPartition) need the cluster and run only when UNIO_CHAOS=1.
package chaos
