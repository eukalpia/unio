# Comparing Unio with MinIO

This document describes how to measure Unio and MinIO on the same hardware
with the same workloads. It is a procedure, not a result. No number in this
repository is a claim about Unio or MinIO until someone has run this procedure,
recorded the environment, and kept the raw output.

## 1. What the targets mean

The targets come from `docs/design/architecture.md`, section 1.1. They are goals
for the v0.2 performance milestone. A target that is not met is a finding to
publish with its data, not a number to round away.

| Metric | Target | Tool in this repository |
|---|---|---|
| PUT and GET 64 MiB throughput | at least MinIO (goal: +10%) | `warp put`, `warp get` (64 MiB objects) |
| PUT 4 KiB ops/s | at least 2x MinIO | `warp put` (4 KiB objects), or `unio-bench -workload put-4k` |
| GET 4 KiB ops/s | at least 1.5x MinIO | `warp get` (4 KiB objects), or `unio-bench -workload get-4k` |
| LIST p99, 1000-key page, 10M-object bucket, prefix and delimiter | at least 10x faster than MinIO | `unio-bench -workload list -list-keys 10000000 -list-page 1000` |
| Memory bounded by configuration, no OOM at 10k connections | load test | not covered by this procedure |

Read the targets this way:

- "Throughput at least MinIO" compares MB/s on the same object size and
  concurrency. Both systems must run with identical `warp` parameters.
- "2x" and "1.5x" compare ops/s for 4 KiB objects. Small objects are dominated by
  per-request cost, so client settings matter as much as the server.
- "10x for LIST p99" compares the p99 latency of one `ListObjectsV2` page of 1000
  keys with a delimiter, in a bucket of 10 million objects. The bucket must hold
  that many objects before the measurement starts, so the pre-fill is the
  dominant cost of this comparison.

## 2. Hardware and topology

Both systems use the 4-node by 4-drive layout: 4 nodes, each with 4 drives. The
Compose files are:

- `deploy/compose/cluster-4x4.yml` for Unio;
- `deploy/compose/minio-4x4.yml` for MinIO.

Both files publish host ports 9001 to 9004, so only one stack may run at a time.
Stop one before starting the other:

```sh
docker compose -f deploy/compose/cluster-4x4.yml down -v
docker compose -f deploy/compose/minio-4x4.yml up -d
```

The `-v` flag removes the named volumes, so each run starts with empty drives.
Check the file for the published S3 port. Use one entry point for the harness,
normally the port the file maps for the first node or a load balancer, and
record which one. Use the same entry point for both systems.

Record the following for every run, in a notes file kept with the results. Do
not store it in the repository unless it is a deliberate report.

| Item | Value to record |
|---|---|
| CPU model and thread count | e.g. AMD Ryzen 9 7950X3D, 32 threads (the author's workstation) |
| RAM | total and the amount available to Docker |
| Storage | drive model and interface for the disk behind Docker; filesystem |
| OS and build | Windows edition and build; WSL distribution and version |
| Docker | Docker Desktop version; engine version |
| Docker resource limits | CPU and memory limits per container, and the Docker Desktop allocation |
| Filesystem for volumes | named volumes inside the WSL VM, or Windows bind mounts (bind mounts are much slower on Windows and must not be mixed between runs) |
| Unio | commit hash, build flags, `unio` image tag |
| MinIO | image tag, for example `minio/minio:RELEASE.<date>` |
| Go and tools | `go version`, `warp --version`, `benchstat` version |
| Harness | `git rev-parse HEAD` of this repository and the exact command line |
| Date and time | start and end of each run, timezone |
| Other load | state that nothing else ran (browsers, builds, backups) |

Run on AC power with the high-performance power plan. Do not run other builds or
containers during a measurement.

## 3. Credentials

The harness reads its credentials from two environment variables only:

- `UNIO_BENCH_ACCESS_KEY`
- `UNIO_BENCH_SECRET_KEY`

They are never flags, so they do not appear in shell history or process lists.
Put the values in a file outside the repository, load it with `set -a; . FILE;
set +a` in your shell, and do not echo it. Use the credentials that the Compose
file for the system under test sets. Never commit them.

## 4. The harness

Build or run the harness from the repository root:

```sh
go run ./bench/cmd/unio-bench -h
```

| Flag | Default | Meaning |
|---|---|---|
| `-endpoint` | required | base URL, for example `http://localhost:9001`; no credentials, query or fragment |
| `-bucket` | `unio-bench-<random>` | created if missing; use a fresh bucket for each run |
| `-workload` | `all` | comma list of `put-4k`, `get-4k`, `put-1m`, `get-1m`, `put-64m`, `get-64m`, `list`, `mixed`, or `all` |
| `-concurrency` | 8 | parallel workers, 1 to 512 |
| `-duration` | 20s | measured time per workload, at least 1s |
| `-warmup` | 5s | unmeasured time per workload |
| `-list-keys` | 10000 | objects pre-filled for `list`, up to 10000000 |
| `-list-page` | 1000 | `MaxKeys` per LIST page, 1 to 1000 |
| `-out` | stdout | JSON result path |
| `-markdown` | none | Markdown table path |
| `-path-style` | true | path-style addressing |
| `-region` | `us-east-1` | signing region |
| `-seed` | 1 | seed for key and operation choices; record it |

Exit codes: 0 success, 1 run failure (partial results are still written),
2 usage or configuration error, 130 interrupted with Ctrl+C (partial results are
written, and the JSON has `"interrupted": true`).

What each workload measures:

- `put-4k`, `get-4k`, `put-1m`, `get-1m`, `put-64m`, `get-64m`: PUT or GET of one
  object size with a fixed keyspace. The keyspace is 1000 keys for 4 KiB and 1 MiB
  objects, and 32 keys for 64 MiB objects to keep the data near 2 GiB. GET
  workloads pre-create their keyspace before the measurement window opens.
- `list`: two operation types, `LIST_DELIMITER` (`Prefix` plus `Delimiter` `/`)
  and `LIST_NO_DELIMITER`. Each request is one page, and each page is one sample.
  Pagination follows `NextContinuationToken`. The layout has 4 prefixes and puts
  every tenth object under an `archive/` sub-prefix, so the delimiter variant sees
  a common prefix. Use `-list-keys` at least `4 * -list-page` to exercise
  pagination.
- `mixed`: 70% GET and 30% PUT of 4 KiB objects. The results are reported per
  operation and as `TOTAL`.

Each operation type has its own row: operations, errors, duration, ops/s, MiB/s,
and p50, p95 and p99 latency in milliseconds. Warmup traffic and requests that
were in flight when the window closed are excluded. Errors are counted, and the
run continues. Client retries are disabled so that every failure reaches the
error counter.

Example, one workload pass at the cluster's single entry point. Use the same
parameters for both systems, and pick the concurrency once and keep it fixed:

```sh
go run ./bench/cmd/unio-bench \
  -endpoint http://localhost:9001 \
  -bucket unio-bench-run1 \
  -workload put-4k,get-4k,mixed \
  -duration 60s -warmup 10s -concurrency 64 \
  -seed 1 \
  -out results/unio-run1.json -markdown results/unio-run1.md
```

LIST at the 10M-object scale of section 1.1:

```sh
go run ./bench/cmd/unio-bench \
  -endpoint http://localhost:9001 -bucket unio-list-10m \
  -workload list -list-keys 10000000 -list-page 1000 \
  -duration 120s -warmup 30s -concurrency 16 \
  -out results/unio-list-10m.json
```

The harness does not delete objects. The pre-fill of 10 million objects can take
hours, and rerunning with the same bucket rewrites them. Delete the bucket between
systems, and record the time it takes.

## 5. warp

`warp` (`github.com/minio/warp`) generates the cluster-level load for the
throughput targets. Install one version and use it for both systems. The flags
below are the ones this procedure relies on, as known when it was written. Check
them against your version before the run:

```sh
warp put --help
warp get --help
warp mixed --help
warp --help
```

Common options to check: `--host` (accepts a comma-separated list of endpoints),
`--access-key`, `--secret-key`, `--bucket`, `--obj.size`, `--concurrent`,
`--duration`, and `--benchdata` (writes raw data for `warp analyze`).

Read the keys from the environment. Do not type them on the command line.

```sh
# 64 MiB throughput
warp put --host localhost:9001 --access-key "$UNIO_BENCH_ACCESS_KEY" \
  --secret-key "$UNIO_BENCH_SECRET_KEY" --bucket warp-unio-64m \
  --obj.size 64MiB --concurrent 16 --duration 120s --benchdata results/warp-put-64m-unio
warp get --host localhost:9001 --access-key "$UNIO_BENCH_ACCESS_KEY" \
  --secret-key "$UNIO_BENCH_SECRET_KEY" --bucket warp-unio-64m \
  --obj.size 64MiB --concurrent 16 --duration 120s --benchdata results/warp-get-64m-unio

# 4 KiB operations per second
warp put --host localhost:9001 --access-key "$UNIO_BENCH_ACCESS_KEY" \
  --secret-key "$UNIO_BENCH_SECRET_KEY" --bucket warp-unio-4k \
  --obj.size 4KiB --concurrent 64 --duration 120s --benchdata results/warp-put-4k-unio
warp get --host localhost:9001 --access-key "$UNIO_BENCH_ACCESS_KEY" \
  --secret-key "$UNIO_BENCH_SECRET_KEY" --bucket warp-unio-4k \
  --obj.size 4KiB --concurrent 64 --duration 120s --benchdata results/warp-get-4k-unio

# Mixed workload, 4 KiB objects
warp mixed --host localhost:9001 --access-key "$UNIO_BENCH_ACCESS_KEY" \
  --secret-key "$UNIO_BENCH_SECRET_KEY" --bucket warp-unio-mixed \
  --obj.size 4KiB --concurrent 64 --duration 120s \
  --get-distrib 70 --put-distrib 30 --delete-distrib 0 --stat-distrib 0 \
  --benchdata results/warp-mixed-unio
```

`warp` does not measure the LIST p99 target in the form this repository needs.
If your `warp` build has a list command, check its help before using it.
Otherwise use `unio-bench -workload list`, as in section 4.

Read the summary of a saved run with `warp analyze results/<name>.csv.zst`
(check `warp analyze --help` for the file name it expects).

Run `warp` with the multi-host form only as a separate, labelled configuration.
A single entry point for both systems keeps the comparison fair.

## 6. Procedure

For each system, in this order: Unio, then MinIO.

1. Stop the other stack and check that ports 9001 to 9004 are free.
2. Start the stack with `up -d`. Wait until every node reports healthy, and run
   one small request to confirm the endpoint works.
3. Record the environment (section 2) in the notes file.
4. Run the harness workloads and the `warp` runs with the parameters above.
   Keep the raw JSON, Markdown and `warp` output.
5. Repeat the whole sequence at least three times for each system, with a fresh
   bucket each time. Keep every run. Do not average away variance.
6. Tear down with `down -v` before the other system starts.

Report the median of the runs and the range for every metric, with the
environment record. Report a target as met only when the lower bound of the
range clears it.

## 7. Go benchmarks and benchstat

The hot-path `go test -bench` benchmarks are compared with `benchstat`. This is
the procedure the architecture document asks for in section 12. Use it for
changes inside the process, not for cluster comparisons.

```sh
go test -run '^$' -bench . -benchmem -count 10 ./bench/... > results/bench-old.txt
# apply the change
go test -run '^$' -bench . -benchmem -count 10 ./bench/... > results/bench-new.txt
benchstat results/bench-old.txt results/bench-new.txt
```

Install `benchstat` as a tool, pinned to one version, outside the module:
`go install golang.org/x/perf/cmd/benchstat@<version>`. Do not add it to
`go.mod`. Run the old and new benchmarks on the same machine, in the same
state, back to back. Read the `p=` values in the output. A difference without a
significant p value is noise. Do not compare runs from different hardware or
power plans.

## 8. Limitations

- Single host. Both clusters run in containers on one machine, so the network is
  the loopback and the drives share one disk. Results describe this topology and
  are not a multi-machine result.
- Docker Desktop on Windows runs the containers in a WSL2 virtual machine. Its
  I/O and CPU accounting differ from a Linux host. Record the limits and keep
  them constant.
- The client is the AWS SDK for Go v2 with retries disabled and checksums
  calculated only when required. Small-object throughput is partly a property of
  the client. Both systems use the same client, but the results still describe
  this client.
- The harness keeps latency samples in memory, up to 8 Mi samples per run, and
  reports how many were dropped. Percentiles are exact nearest-rank values over
  the kept samples.
- Keyspace sizes are fixed (1000 keys, or 32 for 64 MiB). A larger keyspace would
  change the cache behaviour of both systems.
- The harness does not delete the objects it writes.
