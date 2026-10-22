#!/bin/sh
# Entry point of the s3-tests image. It runs the node IDs listed in the subset
# file (mounted at /subset.txt by run.sh) and writes a JUnit report to
# /results/junit.xml. S3TEST_CONF must name the generated configuration file.
set -u

subset=${SUBSET_FILE:-/subset.txt}
if [ ! -r "$subset" ]; then
	echo "run-subset: cannot read $subset" >&2
	exit 2
fi
if [ -z "${S3TEST_CONF:-}" ] || [ ! -r "$S3TEST_CONF" ]; then
	echo "run-subset: S3TEST_CONF must name a readable configuration file" >&2
	exit 2
fi

while IFS= read -r line || [ -n "$line" ]; do
	case "$line" in
	'' | '#'*) continue ;;
	esac
	set -- "$@" "$line"
done < "$subset"

mkdir -p /results
exec pytest -p no:cacheprovider --junitxml=/results/junit.xml "$@"
