#!/bin/bash
# bench.sh <system: unio-base|unio-new|minio> <label> <workloads> <concurrency> [duration]
set -u
B=$HOME/ubench; SYS=$1; L=$2; WL=$3; C=$4; D=${5:-15s}
R=/var/tmp/ubench-data; rm -rf $R; mkdir -p $R/d1 $R/d2 $R/d3 $R/d4
PW=$(openssl rand -hex 16); export UNIO_BENCH_ACCESS_KEY=benchadminroot1 UNIO_BENCH_SECRET_KEY=$PW
if [ "$SYS" = minio ]; then
  MINIO_CI_CD=1 MINIO_ROOT_USER=benchadminroot1 MINIO_ROOT_PASSWORD=$PW $B/minio server --quiet --address 127.0.0.1:19500 --console-address 127.0.0.1:19501 "$R/d{1...4}" > $B/srv.log 2>&1 &
  RDY=/minio/health/cluster
else
  UNIO_ENDPOINTS="https://localhost:19600$R/d{1...4}" UNIO_NODE_ID=localhost UNIO_S3_ADDRESS=127.0.0.1:19500 UNIO_INTERNODE_ADDRESS=127.0.0.1:19600 \
  UNIO_REQUIRE_TLS=false UNIO_LOG_LEVEL=warn UNIO_ROOT_USER=benchadminroot1 UNIO_ROOT_PASSWORD=$PW UNIO_CLUSTER_SECRET=$(openssl rand -base64 48 | tr -d '\n') $B/$SYS server > $B/srv.log 2>&1 &
  RDY=/unio/health/ready
fi
PID=$!
for i in $(seq 1 120); do curl -sf -o /dev/null http://127.0.0.1:19500$RDY && break; sleep 0.5; done
sleep 2
$B/unio-bench -endpoint http://127.0.0.1:19500 -workload $WL -concurrency $C -duration $D -warmup 3s -list-keys 20000 -markdown $B/out/$L.md -out $B/out/$L.json > $B/out/$L.log 2>&1 || tail -3 $B/out/$L.log
kill $PID; wait $PID 2>/dev/null
rm -rf $R
grep "^| [plgm]" $B/out/$L.md | awk -F'|' -v L="$L" '{printf "%-16s %-8s %-18s ops/s=%-9s MiB/s=%-9s p50=%-9s p99=%-9s err=%s\n",L,$2,$3,$7,$8,$9,$11,$5}'
