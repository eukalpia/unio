# unio-bench results

- Schema version: 1
- Endpoint: http://127.0.0.1:19500
- Bucket: unio-bench-ff0f3837
- Started: 2026-11-25T21:28:06Z
- Finished: 2026-11-25T21:29:59Z
- Interrupted: false
- Concurrency: 16, duration 15s, warmup 3s, seed 1
- Client: go1.27.4 linux/amd64, 4 CPUs

| Workload | Operation | Ops | Errors | Duration (s) | Ops/s | MiB/s | p50 (ms) | p95 (ms) | p99 (ms) |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| put-4k | PUT | 127680 | 0 | 15.00 | 8512.0 | 33.25 | 1.621 | 3.798 | 5.826 |
| get-4k | GET | 239434 | 0 | 15.00 | 15962.3 | 62.35 | 0.779 | 2.547 | 3.601 |
| put-1m | PUT | 5559 | 0 | 15.00 | 370.6 | 370.60 | 42.415 | 63.011 | 74.442 |
| get-1m | GET | 55177 | 0 | 15.00 | 3678.5 | 3678.47 | 3.996 | 8.572 | 11.296 |
| list | LIST_DELIMITER | 1998 | 0 | 15.00 | 133.2 | 0.00 | 52.947 | 110.995 | 143.365 |
| list | LIST_NO_DELIMITER | 1980 | 0 | 15.00 | 132.0 | 0.00 | 58.914 | 114.814 | 147.485 |
| list | TOTAL | 3978 | 0 | 15.00 | 265.2 | 0.00 | 56.410 | 113.371 | 144.896 |
| mixed | GET | 121078 | 0 | 15.00 | 8071.9 | 31.53 | 0.874 | 3.079 | 4.326 |
| mixed | PUT | 51312 | 0 | 15.00 | 3420.8 | 13.36 | 1.682 | 3.884 | 5.271 |
| mixed | TOTAL | 172390 | 0 | 15.00 | 11492.7 | 44.89 | 1.087 | 3.400 | 4.720 |
