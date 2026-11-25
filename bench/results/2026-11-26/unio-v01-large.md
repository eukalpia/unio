# unio-bench results

- Schema version: 1
- Endpoint: http://127.0.0.1:19500
- Bucket: unio-bench-660ea1b5
- Started: 2026-11-25T21:30:04Z
- Finished: 2026-11-25T21:30:43Z
- Interrupted: false
- Concurrency: 4, duration 15s, warmup 3s, seed 1
- Client: go1.27.4 linux/amd64, 4 CPUs

| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| put-64m | PUT | 152 | 0 | 15.00 | 10.1 | 648.53 | 385.228 | 421.062 | 453.195 |
| get-64m | GET | 1549 | 0 | 15.00 | 103.3 | 6609.07 | 38.542 | 47.567 | 52.211 |
