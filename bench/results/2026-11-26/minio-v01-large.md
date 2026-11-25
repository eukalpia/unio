# unio-bench results

- Schema version: 1
- Endpoint: http://127.0.0.1:19500
- Bucket: unio-bench-78a9ecc1
- Started: 2026-11-25T21:33:28Z
- Finished: 2026-11-25T21:34:07Z
- Interrupted: false
- Concurrency: 4, duration 15s, warmup 3s, seed 1
- Client: go1.27.4 linux/amd64, 4 CPUs

| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| put-64m | PUT | 151 | 0 | 15.00 | 10.1 | 644.27 | 385.651 | 456.057 | 483.022 |
| get-64m | GET | 1281 | 0 | 15.00 | 85.4 | 5465.60 | 46.034 | 56.289 | 63.551 |
