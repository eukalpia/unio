# unio-bench results

- Schema version: 1
- Endpoint: http://127.0.0.1:19500
- Bucket: unio-bench-d7d23c64
- Started: 2026-11-25T21:34:12Z
- Finished: 2026-11-25T21:34:51Z
- Interrupted: false
- Concurrency: 4, duration 15s, warmup 3s, seed 1
- Client: go1.27.4 linux/amd64, 4 CPUs

| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| put-64m | PUT | 157 | 0 | 15.00 | 10.5 | 669.87 | 376.219 | 406.143 | 422.941 |
| get-64m | GET | 1529 | 0 | 15.00 | 101.9 | 6523.73 | 38.731 | 47.433 | 54.028 |
