# Chaos harness

Crash and partition scenarios for a local Unio cluster, plus the harness pieces
(cluster control, network partitions, drive faults, a ledger of acknowledged
writes, and an invariant verifier). The unit tests need neither Docker nor a
running cluster. The cluster scenarios run only when `UNIO_CHAOS=1`.

## Prerequisites

- Docker Engine with the `docker compose` v2 plugin, on the `PATH` of the test
  process.
- The cluster started from the repository root:

  ```sh
  docker compose -f deploy/compose/cluster-4x4.yml --env-file deploy/compose/.env up -d --build
  ```

  Services `node1` to `node4` must answer on `http://localhost:9001` to
  `http://localhost:9004`, and the readiness endpoint
  `/unio/health/ready` must return 200.
- Credentials for the test client, in `UNIO_TEST_ACCESS_KEY` and
  `UNIO_TEST_SECRET_KEY`.
- The compose file must not set a `restart:` policy on the node services. Docker
  restarts a container killed with SIGKILL under such a policy, and the
  scenario then fails with "still running after SIGKILL" to show that the crash
  did not take effect.
- The `alpine:3.20` image, only if drive-fault helpers are used.

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `UNIO_CHAOS` | unset | Set to `1` to run the cluster scenarios. |
| `UNIO_TEST_ACCESS_KEY` | required | Access key of the test client. Never logged. |
| `UNIO_TEST_SECRET_KEY` | required | Secret key of the test client. Never logged. |
| `UNIO_CHAOS_ENDPOINTS` | `http://localhost:9001,...,http://localhost:9004` | Comma-separated base URLs, ordered node1 first. |
| `UNIO_CHAOS_COMPOSE_FILE` | `../../deploy/compose/cluster-4x4.yml` | Compose file, resolved against the package directory. |
| `UNIO_CHAOS_PROJECT` | `unio-4x4` | Compose project name. Prefixes volumes and the `_internal` network. |
| `UNIO_CHAOS_BUCKET` | `unio-chaos` | Bucket that receives the workload. Created if missing. |
| `UNIO_CHAOS_DURATION` | `2m` | Length of the random kill phase of `TestDurability`. |
| `UNIO_CHAOS_SEED` | random | Seed for the kill schedule and payloads. Logged on every run. |

## Commands

Unit tests (no Docker, no cluster):

```sh
go test -race -count=1 ./test/chaos/...
```

Durability scenario (random kills, then a simultaneous kill of all nodes):

```sh
UNIO_CHAOS=1 go test -count=1 -v -run TestDurability -timeout 30m ./test/chaos/
```

Partition scenario (one node is removed from `unio-4x4_internal`, then restored):

```sh
UNIO_CHAOS=1 go test -count=1 -v -run TestPartition -timeout 30m ./test/chaos/
```

To replay a failing run, set `UNIO_CHAOS_SEED` to the seed printed in the log.
The kill schedule and the object bodies are then identical. The run ID that
namespaces the keys is still random.

## What the scenarios check

Every PUT is recorded in a ledger with its SHA-256 and length. Each PUT is
classified by what the server answered:

- **acked**: HTTP 200. Invariant I1 applies: after the whole cluster has been
  killed with SIGKILL and restarted, the object must read back with the
  recorded length and SHA-256. A mismatch is I2, a missing object is I1.
- **rejected**: an explicit 4xx or 5xx. Invariant I4 applies: the key must
  return `NoSuchKey` afterwards and never become visible. A `NoSuchBucket` or
  any other error does not confirm absence, so it is reported as a violation.
- **unknown**: a transport error or a timeout after the request may have
  reached the server. Neither I1 nor I4 can be claimed, so the write is counted
  in the report but never checked. A high unknown count points at the cluster
  or the client being unavailable, not at data loss.

The SDK retry policy is disabled, so each classified outcome corresponds to one
server response. A run that acknowledges no writes fails, because otherwise
the verifier would pass vacuously, for example when credentials are wrong.

Verification reads through every node in turn (I3 end to end). It runs only
after all nodes answer their readiness endpoint.

## What the harness cannot cover yet

- **Drive faults in scenarios.** `Faults.Corrupt` and `Faults.Delete` flip a
  byte or remove a file on a drive volume, using a helper container. Only the
  command assembly is unit tested. No scenario uses them yet, because the
  on-disk shard layout is not fixed. A volume-level flip is also weaker than
  a fault injected by the node, which would exercise the bitrot check and heal
  on read more precisely. That needs an admin fault API on the node.
- **Slow drives** and error injection inside the drive engine. These need node
  hooks.
- **Raft leader failure with linearizability checking** (porcupine). That is a
  later milestone. `TestDurability` does not check linearizable histories.
- **Heal on read.** The verifier reads each object and checks its digest, but
  it does not observe whether a corrupted shard was repaired.
- **Clock and disk-full faults.** Not covered.
