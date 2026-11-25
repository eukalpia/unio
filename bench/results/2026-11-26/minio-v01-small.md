# unio-bench results

- Schema version: 1
- Endpoint: http://127.0.0.1:19500
- Bucket: unio-bench-6aad4ab4
- Started: 2026-11-25T21:31:01Z
- Finished: 2026-11-25T21:33:15Z
- Interrupted: false
- Concurrency: 16, duration 15s, warmup 3s, seed 1
- Client: go1.27.4 linux/amd64, 4 CPUs

| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| put-4k | PUT | 12013 | 0 | 15.00 | 800.9 | 3.13 | 19.396 | 32.458 | 42.613 |
| get-4k | GET | 97669 | 0 | 15.00 | 6511.3 | 25.43 | 2.134 | 5.348 | 8.052 |
| put-1m | PUT | 3384 | 0 | 15.00 | 225.6 | 225.60 | 70.593 | 103.127 | 118.209 |
| get-1m | GET | 36052 | 0 | 15.00 | 2403.5 | 2403.47 | 6.062 | 13.506 | 18.428 |
| list | LIST_DELIMITER | 508 | 0 | 15.00 | 33.9 | 0.00 | 219.522 | 621.231 | 853.903 |
| list | LIST_NO_DELIMITER | 469 | 97 | 15.00 | 31.3 | 0.00 | 216.939 | 836.043 | 859.109 |
| list | TOTAL | 977 | 97 | 15.00 | 65.1 | 0.00 | 217.713 | 825.086 | 859.109 |
| mixed | GET | 17980 | 0 | 15.00 | 1198.7 | 4.68 | 4.919 | 19.311 | 30.189 |
| mixed | PUT | 7681 | 0 | 15.00 | 512.1 | 2.00 | 13.147 | 30.057 | 37.597 |
| mixed | TOTAL | 25661 | 0 | 15.00 | 1710.7 | 6.68 | 7.879 | 24.719 | 34.505 |
