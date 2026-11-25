# End-to-end harness

`run.sh` starts Unio in Docker Compose, waits until every node reports ready,
runs the test suites against the stack, and tears the stack down, even when a
step fails or you press Ctrl+C.

## Requirements

- Docker with Compose v2 (`docker compose version`), and the daemon running.
- Go (the version in `go.mod`), `curl`, `openssl` and `bash`. On Windows, run
  the script from WSL.
- Ports 9001 to 9004 free on 127.0.0.1. The cluster, the single node and the
  RustFS comparison stack all publish these ports, so stop one stack before you
  start another.

## Usage

From the repository root:

```sh
test/e2e/run.sh single                       # one node, four drives; integration and security
test/e2e/run.sh cluster                      # 4 nodes x 4 drives; integration and security
test/e2e/run.sh cluster integration chaos    # chaos needs the cluster
```

The suites are:

- `integration`: S3 behaviour, from `test/integration`.
- `security`: the attack suite, from `test/security`.
- `chaos`: crash and partition scenarios, from `test/chaos`. Cluster only.

With no suite argument, `integration` and `security` run.

## What the script does

1. Generates the local credentials for this run (see below), then clears any
   containers and volumes left by an earlier run and builds the image from
   `deploy/docker/Dockerfile`.
2. Runs `docker compose up -d` for the stack.
3. Waits for `/unio/health/ready` on every node, with one shared deadline set by
   `UNIO_E2E_READY_TIMEOUT` (default 180 seconds). If a container exits or the
   deadline passes, it prints the container logs and fails.
4. Exports the `UNIO_TEST_*` variables. The target is node 1 at
   `http://127.0.0.1:9001`, with the root user and password as credentials.
5. Runs `go test -count=1 -v -timeout 30m ./test/<suite>/...` for each suite.
   For `chaos` it also sets `UNIO_CHAOS=1`, the compose file, the project name
   and the four endpoints.
6. Runs `docker compose down --volumes` on exit, on success, failure and
   interrupt alike.

## Logs

When a suite fails, its output is kept in `test/e2e/out/<mode>-<suite>.log`, and
the logs of every container in the stack go to `test/e2e/out/<mode>-compose.log`.
The directory is git-ignored. A passing run leaves nothing behind.

## Credentials

Each run generates a new root user, root password and cluster secret. They
exist only in the environment of the script, in the containers of that run and
in the suites. Nothing is written to disk, and nothing is reused between runs.

To run with fixed values, for example to reproduce a failure, create a file
with `unio init --output <file>`, then point the script at it. The file must
set `UNIO_ROOT_USER`, `UNIO_ROOT_PASSWORD` and `UNIO_CLUSTER_SECRET`. The
metrics token that `unio init` also writes is not used by the stacks:

```sh
UNIO_E2E_ENV_FILE=/path/to/my.env test/e2e/run.sh single
```

The rules for each value are in `internal/config/credentials.go`.

## Variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `UNIO_E2E_ENV_FILE` | unset | File that sets the three credentials. Unset means random values per run. |
| `UNIO_E2E_READY_TIMEOUT` | `180` | Seconds to wait for readiness on all nodes. |
| `UNIO_TEST_SLOW` | unset | Set to `1` to run the slow abuse test in `test/security`. It is skipped otherwise. |

The suites read their own variables from the environment. See
`test/integration/README.md`, `test/security/README.md` and
`test/chaos/README.md`.

## Running a stack by hand

`deploy/compose/.env.example` is a template with placeholders. Compose reads
`deploy/compose/.env`, so copy the template and set its values first, for
example with `unio init --output deploy/compose/.env`. Then:

```sh
docker compose -p unio-single -f deploy/compose/single.yml up -d --build
set -a; . deploy/compose/.env; set +a
export UNIO_TEST_ENDPOINT=http://127.0.0.1:9001
export UNIO_TEST_ACCESS_KEY=$UNIO_ROOT_USER
export UNIO_TEST_SECRET_KEY=$UNIO_ROOT_PASSWORD
go test -count=1 -v ./test/integration/...
docker compose -p unio-single -f deploy/compose/single.yml down --volumes
```

## Current state

The node binary does not serve S3 yet: `unio server` returns "not yet available"
until the node assembly lands. Until then the containers exit when they start,
and `run.sh` fails at the readiness step and prints the container logs. The
configuration in this tree also still requires `UNIO_METRICS_TOKEN`, which the
stacks do not set. The configuration work makes that token optional.
