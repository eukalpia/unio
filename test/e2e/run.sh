#!/usr/bin/env bash
#
# End-to-end harness. It starts a single-node Unio or a 4-node cluster with
# Docker Compose, waits until every node reports ready, runs the test suites
# against it, and tears the stack down even when a step fails.
#
# Usage, from any directory:
#   test/e2e/run.sh single|cluster [integration] [security] [chaos]
#
# With no suite, integration and security run. The chaos suite kills and
# partitions nodes, so it needs the 4-node cluster. See test/e2e/README.md.
#
# Environment:
#   UNIO_E2E_ENV_FILE       file that sets UNIO_ROOT_USER, UNIO_ROOT_PASSWORD
#                           and UNIO_CLUSTER_SECRET. By default the script
#                           generates new random values for every run.
#   UNIO_E2E_READY_TIMEOUT  seconds to wait for readiness (default: 180)

set -euo pipefail

usage="usage: $0 single|cluster [integration] [security] [chaos]"

die() {
	echo "e2e: $*" >&2
	exit 2
}

log() {
	echo "e2e: $*"
}

if (( $# < 1 )); then
	echo "$usage" >&2
	exit 2
fi
mode=$1
shift

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
out_dir="$script_dir/out"
ready_timeout=${UNIO_E2E_READY_TIMEOUT:-180}

[[ $ready_timeout =~ ^[0-9]+$ ]] || die "UNIO_E2E_READY_TIMEOUT must be a number of seconds, got '$ready_timeout'"

case "$mode" in
single)
	compose_file="$repo_root/deploy/compose/single.yml"
	project=unio-single
	endpoints=(http://127.0.0.1:9001)
	;;
cluster)
	compose_file="$repo_root/deploy/compose/cluster-4x4.yml"
	project=unio-4x4
	endpoints=(http://127.0.0.1:9001 http://127.0.0.1:9002 http://127.0.0.1:9003 http://127.0.0.1:9004)
	;;
*)
	echo "e2e: unknown mode '$mode'" >&2
	echo "$usage" >&2
	exit 2
	;;
esac

if (( $# == 0 )); then
	set -- integration security
fi
for suite in "$@"; do
	case "$suite" in
	integration | security) ;;
	chaos)
		[[ $mode == cluster ]] || die "the chaos suite needs the 4-node cluster: use 'run.sh cluster chaos'"
		;;
	*) die "unknown suite '$suite' (want integration, security or chaos)" ;;
	esac
done

for tool in docker curl go openssl; do
	command -v "$tool" >/dev/null 2>&1 || die "$tool is required on PATH"
done
docker compose version >/dev/null 2>&1 || die "docker compose v2 is required"

# The local credentials are random for every run, so no secret is stored in
# the repository or reused between runs. They are exported, so that compose
# passes them to the containers as secrets and the suites read them. A file
# given in UNIO_E2E_ENV_FILE replaces them.
if [[ -n ${UNIO_E2E_ENV_FILE:-} ]]; then
	[[ -r $UNIO_E2E_ENV_FILE ]] || die "cannot read UNIO_E2E_ENV_FILE=$UNIO_E2E_ENV_FILE"
	set -a
	# shellcheck disable=SC1090
	. "$UNIO_E2E_ENV_FILE"
	set +a
	: "${UNIO_ROOT_USER:?$UNIO_E2E_ENV_FILE must set UNIO_ROOT_USER}"
	: "${UNIO_ROOT_PASSWORD:?$UNIO_E2E_ENV_FILE must set UNIO_ROOT_PASSWORD}"
	: "${UNIO_CLUSTER_SECRET:?$UNIO_E2E_ENV_FILE must set UNIO_CLUSTER_SECRET}"
else
	UNIO_ROOT_USER=$(openssl rand -hex 10)      # 20 letters and digits
	UNIO_ROOT_PASSWORD=$(openssl rand -hex 24)  # 48 hex digits
	UNIO_CLUSTER_SECRET=$(openssl rand -hex 32) # 64 hex digits
	export UNIO_ROOT_USER UNIO_ROOT_PASSWORD UNIO_CLUSTER_SECRET
fi

# The suites read the target and the root credentials from these variables.
export UNIO_TEST_ENDPOINT=${endpoints[0]}
export UNIO_TEST_ACCESS_KEY=$UNIO_ROOT_USER
export UNIO_TEST_SECRET_KEY=$UNIO_ROOT_PASSWORD
export UNIO_TEST_REGION=us-east-1

compose() {
	docker compose -p "$project" -f "$compose_file" "$@"
}

logs_dumped=0
dump_logs() {
	(( logs_dumped == 0 )) || return 0
	logs_dumped=1
	mkdir -p "$out_dir"
	compose logs --no-color --timestamps >"$out_dir/$mode-compose.log" 2>&1 || true
	log "container logs saved to $out_dir/$mode-compose.log"
}

# cleanup always runs, through the EXIT trap below, so that the stack is torn
# down on success, on failure and on interrupt.
# shellcheck disable=SC2329
cleanup() {
	local status=$?
	trap - EXIT
	set +e
	if (( status != 0 )); then
		dump_logs
	fi
	log "tearing down $project"
	if ! compose down --volumes --remove-orphans --timeout 10 >&2; then
		echo "e2e: warning: docker compose down failed; check for leftover containers of project $project" >&2
	fi
	if (( status == 0 )); then
		rmdir "$out_dir" 2>/dev/null || true
	fi
	exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

# Clear containers and volumes left by an earlier run that did not finish.
compose down --volumes --remove-orphans >/dev/null 2>&1 || true

log "building the image for $mode"
if ! compose build; then
	echo "e2e: docker compose build failed for $mode" >&2
	exit 1
fi

log "starting $mode ($project)"
if ! compose up -d; then
	echo "e2e: docker compose up failed for $mode; the containers are removed by the teardown" >&2
	exit 1
fi

# wait_ready polls the readiness endpoint of every node with one shared
# deadline. It stops early when a container has exited, because a node that
# exited will not become ready.
wait_ready() {
	local deadline=$(( SECONDS + ready_timeout )) endpoint
	for endpoint in "${endpoints[@]}"; do
		until curl -fsS --max-time 3 "$endpoint/unio/health/ready" >/dev/null 2>&1; do
			if [[ -n $(compose ps --status exited --quiet) ]]; then
				echo "e2e: a container exited while waiting for $endpoint" >&2
				return 1
			fi
			if (( SECONDS >= deadline )); then
				echo "e2e: $endpoint/unio/health/ready did not answer 200 within ${ready_timeout}s" >&2
				return 1
			fi
			sleep 2
		done
		log "ready: $endpoint"
	done
}

if ! wait_ready; then
	dump_logs
	echo "e2e: container logs follow" >&2
	cat -- "$out_dir/$mode-compose.log" >&2
	exit 1
fi

# run_suite runs one suite in its own package tree. Its output goes to the
# terminal and to a log file, which is kept only when the suite fails.
run_suite() {
	local suite=$1
	local log_file="$out_dir/$mode-$suite.log"
	mkdir -p "$out_dir"
	log "running the $suite suite against $mode"
	if (
		cd "$repo_root"
		if [[ $suite == chaos ]]; then
			export UNIO_CHAOS=1
			export UNIO_CHAOS_COMPOSE_FILE=$compose_file
			export UNIO_CHAOS_PROJECT=$project
			UNIO_CHAOS_ENDPOINTS=$(IFS=,; echo "${endpoints[*]}")
			export UNIO_CHAOS_ENDPOINTS
		fi
		go test -count=1 -v -timeout 30m "./test/$suite/..." 2>&1 | tee "$log_file"
	); then
		rm -f "$log_file"
		log "$suite suite passed"
		return 0
	fi
	echo "e2e: the $suite suite failed against $mode; its output is in $log_file" >&2
	return 1
}

failed=0
for suite in "$@"; do
	run_suite "$suite" || failed=1
done
exit "$failed"
