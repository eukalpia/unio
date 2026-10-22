#!/bin/sh
# Runs the ceph/s3-tests v0.1 subset against an S3 endpoint inside Docker and
# prints the pass rate. README.md in this directory lists the requirements,
# the variables and the rules for the pass rate.
#
# The configuration file for ceph/s3-tests is generated into a temporary
# directory outside the repository and is removed when the script exits.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
repo=$(cd "$here/../../.." && pwd)

# host_path prints a path in the form the Docker CLI and native binaries need.
# On Windows (Git Bash) that is the mixed C:/ form that cygpath -m produces.
host_path() {
	if command -v cygpath >/dev/null 2>&1; then
		cygpath -m "$1"
	else
		printf '%s\n' "$1"
	fi
}

: "${UNIO_TEST_ENDPOINT:?set UNIO_TEST_ENDPOINT, for example http://localhost:9001}"
: "${UNIO_TEST_ACCESS_KEY:?set UNIO_TEST_ACCESS_KEY}"
: "${UNIO_TEST_SECRET_KEY:?set UNIO_TEST_SECRET_KEY}"

ref=${S3TESTS_REF:-5b3987116eca0b4b8fa57d6b10f3db57bab196b6}
image=${S3TESTS_IMAGE:-}
target=${S3TESTS_TARGET:-0.90}
region=${UNIO_TEST_REGION:-us-east-1}
ca_file=${UNIO_TEST_CA_FILE:-}
results_dir=${S3TESTS_RESULTS_DIR:-}

# Split the endpoint into scheme, host and port. Only a bare base URL is
# accepted, and the URL is never echoed back, in case it carries credentials.
endpoint=${UNIO_TEST_ENDPOINT%/}
case "$endpoint" in
http://*)
	secure=False
	verify=False
	rest=${endpoint#http://}
	port=80
	;;
https://*)
	secure=True
	verify=True
	rest=${endpoint#https://}
	port=443
	;;
*)
	echo "run.sh: UNIO_TEST_ENDPOINT must start with http:// or https://" >&2
	exit 2
	;;
esac
case "$rest" in
*/* | *\?* | *@*)
	echo "run.sh: UNIO_TEST_ENDPOINT must be a bare base URL (no path, query or credentials)" >&2
	exit 2
	;;
esac
host=${rest%%:*}
case "$rest" in
*:*) port=${rest##*:} ;;
esac

# Containers reach the host through host.docker.internal, not localhost.
case "$host" in
localhost | 127.0.0.1) container_host=host.docker.internal ;;
*) container_host=$host ;;
esac

# An empty api_name skips the location test, because S3 rejects an explicit
# us-east-1 location constraint.
api_name=
if [ "$region" != us-east-1 ]; then
	api_name=$region
fi

ca_env=
if [ -n "$ca_file" ]; then
	if [ "$secure" != True ]; then
		echo "run.sh: UNIO_TEST_CA_FILE only applies to an https endpoint" >&2
		exit 2
	fi
	if [ ! -r "$ca_file" ]; then
		echo "run.sh: UNIO_TEST_CA_FILE is not readable" >&2
		exit 2
	fi
fi

if [ -z "$results_dir" ]; then
	results_dir=$(mktemp -d)
fi
mkdir -p "$results_dir"
results_dir=$(cd "$results_dir" && pwd)

conf_dir=$(mktemp -d)
trap 'rm -rf "$conf_dir"' EXIT
chmod 700 "$conf_dir"
umask 077

cat > "$conf_dir/s3tests.conf" <<EOF
[DEFAULT]
host = $container_host
port = $port
is_secure = $secure
ssl_verify = $verify

[fixtures]
bucket prefix = unio-s3t-{random}-

[s3 main]
display_name = unio-test
user_id = unio-test
email = unio-test@example.invalid
api_name = $api_name
access_key = $UNIO_TEST_ACCESS_KEY
secret_key = $UNIO_TEST_SECRET_KEY

EOF

# The upstream loader refuses to start without these identity sections. They
# all repeat the single account, because the tests that need a second account,
# a tenant or IAM are not in the subset.
for section in "s3 alt" "s3 tenant" "iam" "iam root" "iam alt root"; do
	name=$(printf '%s' "$section" | tr ' ' '-')
	cat >> "$conf_dir/s3tests.conf" <<EOF

[$section]
display_name = unio-test-$name
user_id = unio-test-$name
email = unio-test-$name@example.invalid
tenant = unio-test
access_key = $UNIO_TEST_ACCESS_KEY
secret_key = $UNIO_TEST_SECRET_KEY
EOF
done

if [ -n "$ca_file" ]; then
	cp "$ca_file" "$conf_dir/ca.pem"
	# botocore reads AWS_CA_BUNDLE when verification is on.
	ca_env="-e AWS_CA_BUNDLE=/conf/ca.pem"
fi

if [ -n "$image" ]; then
	if ! docker image inspect "$image" >/dev/null 2>&1; then
		docker pull "$image"
	fi
else
	image="unio-s3tests:$(printf '%s' "$ref" | cut -c1-12)"
	docker build --build-arg "S3TESTS_REF=$ref" -t "$image" "$(host_path "$here")"
fi

set +e
# shellcheck disable=SC2086  # $ca_env is either empty or one word pair.
MSYS_NO_PATHCONV=1 docker run --rm \
	--add-host=host.docker.internal:host-gateway \
	-e S3TEST_CONF=/conf/s3tests.conf \
	$ca_env \
	-v "$(host_path "$conf_dir"):/conf:ro" \
	-v "$(host_path "$here/subset.txt"):/subset.txt:ro" \
	-v "$(host_path "$results_dir"):/results" \
	"$image"
status=$?
set -e

if [ ! -s "$results_dir/junit.xml" ]; then
	echo "run.sh: the run wrote no JUnit report (pytest exit status $status)" >&2
	exit 2
fi
echo "pytest exit status: $status; JUnit report: $results_dir/junit.xml"

summary_status=0
(cd "$repo" && go run ./test/integration/s3tests/summarize \
	-results "$(host_path "$results_dir/junit.xml")" \
	-subset "$(host_path "$here/subset.txt")" \
	-target "$target") || summary_status=$?
exit "$summary_status"
