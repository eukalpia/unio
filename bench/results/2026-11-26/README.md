# Unio 0.1.0 against MinIO, 2026-11-26

Both servers ran one after the other on the same machine, with the same drives,
the same client and the same parameters. Nothing else ran on the machine apart
from an idle web application.

## Environment

| | |
|---|---|
| Machine | VMware virtual machine, 4 vCPU Intel Xeon Gold 6544Y, 8 GiB RAM |
| Disk | one 200 GB virtual SSD; the four drive directories share it |
| OS | Ubuntu 24.04.4 LTS |
| Unio | commit `7fef5b4` (0.1.0), 1 node x 4 drives, Reed-Solomon 2+2, default settings |
| MinIO | `DEVELOPMENT.2025-05-24T17-08-30Z` (the binary from the `bitnamilegacy/minio` image; MinIO no longer publishes binaries), 1 node x 4 drives, default erasure set |
| Client | `unio-bench` from this repository, on the same machine, over loopback |

Every run starts from empty drives: [`bench.sh`](bench.sh) creates the
directories, starts the server, runs `unio-bench` with a 3 s warmup and 15 s
of measurement, then stops the server and deletes the data.

```sh
bash bench.sh unio-v01 unio-v01-small  put-4k,get-4k,put-1m,get-1m,list,mixed 16
bash bench.sh unio-v01 unio-v01-large  put-64m,get-64m 4
bash bench.sh minio    minio-v01-small put-4k,get-4k,put-1m,get-1m,list,mixed 16
bash bench.sh minio    minio-v01-large put-64m,get-64m 4
```

## Results

| Workload | Concurrency | MinIO | Unio | Unio / MinIO |
|---|---:|---:|---:|---:|
| PUT 4 KiB | 16 | 801 ops/s, p99 42.6 ms | 8,512 ops/s, p99 5.8 ms | 10.6x |
| GET 4 KiB | 16 | 6,511 ops/s, p99 8.1 ms | 15,962 ops/s, p99 3.6 ms | 2.5x |
| PUT 1 MiB | 16 | 226 MiB/s, p99 118 ms | 371 MiB/s, p99 74 ms | 1.6x |
| GET 1 MiB | 16 | 2,403 MiB/s, p99 18.4 ms | 3,678 MiB/s, p99 11.3 ms | 1.5x |
| LIST, 20,000 keys | 16 | 65 ops/s, p99 859 ms, 97 errors | 265 ops/s, p99 145 ms, 0 errors | 4.1x |
| Mixed GET and PUT, 4 KiB | 16 | 1,711 ops/s, p99 34.5 ms | 11,493 ops/s, p99 4.7 ms | 6.7x |
| PUT 64 MiB | 4 | 644 MiB/s | 649 and 670 MiB/s | about equal |
| GET 64 MiB | 4 | 5,466 MiB/s | 6,609 and 6,524 MiB/s | 1.2x |

Unio's large-object run was repeated (`unio-v01-large2`); both runs are listed.
PUT 64 MiB is limited by the disk for both servers. The 97 MinIO LIST errors
were all in the listing without a delimiter; `unio-bench` counts failed
requests but does not record their cause.

## What this does not show

- One machine, one disk. A real deployment spreads drives over disks and
  nodes; this run says nothing about network cost or about recovery.
- Short runs on empty stores. A store that has been written to for months
  behaves differently.
- One MinIO build with default settings. MinIO can be tuned.

The raw output of every run is next to this file: `*.md` (tables), `*.json`
(all numbers) and `*.log`.
