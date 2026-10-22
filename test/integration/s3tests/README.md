# ceph/s3-tests: v0.1 subset

This directory runs the v0.1 subset of [ceph/s3-tests](https://github.com/ceph/s3-tests)
against an S3 endpoint and reports the pass rate. The architecture target
(section 1.1) is at least 90% of the subset.

Upstream notice: the ceph/s3-tests repository says its tests have moved into
ceph/ceph and will no longer be maintained there. The suite is pinned to the
last commit of the repository, so runs stay reproducible.

## Files

- `run.sh`: builds or pulls the image, writes the configuration to a temporary
  directory, runs the subset, and prints the summary. Run it with `sh`.
- `Dockerfile`: installs ceph/s3-tests at `S3TESTS_REF` on `python:3.12-slim`.
- `run-subset.sh`: the container entry point. It runs the node IDs in
  `subset.txt` and writes `junit.xml`.
- `subset.txt`: the 203 node IDs in scope, with the excluded areas in its header.
- `summarize/`: a Go program that reads the JUnit report and the subset and
  prints the pass rates. It has unit tests.

## Requirements

- Docker. On Windows, Docker Desktop with Git Bash works.
- Network access during the image build: `github.com` (the source archive at
  `S3TESTS_REF`) and PyPI (the packages in that commit's `requirements.txt`).
  The packages are not pinned, so a rebuild can pick up newer versions.
- Go 1.26 or later on `PATH`, for the summarizer. `run.sh` runs it with `go run`.
- A reachable S3 endpoint.

## Running

```sh
UNIO_TEST_ENDPOINT=http://localhost:9001 \
UNIO_TEST_ACCESS_KEY=... \
UNIO_TEST_SECRET_KEY=... \
sh test/integration/s3tests/run.sh
```

The script exits 0 when the subset pass rate reaches `S3TESTS_TARGET`, 1 when it
does not, and 2 on a configuration or input error. The exit status of pytest
itself is printed but does not decide the result, since failing tests are
expected while the implementation matures.

`localhost` and `127.0.0.1` in the endpoint are mapped to
`host.docker.internal` inside the container.

## Variables

| Variable | Default | Meaning |
|---|---|---|
| `UNIO_TEST_ENDPOINT` | required | Base URL, as for the Go suite. |
| `UNIO_TEST_ACCESS_KEY` | required | Access key ID. Written only to the temporary configuration file. |
| `UNIO_TEST_SECRET_KEY` | required | Secret access key. Same handling. Must not contain `%`, because the upstream configuration parser interpolates it. |
| `UNIO_TEST_REGION` | `us-east-1` | Region. For a region other than `us-east-1` the configuration sets `api_name`, so the location test runs. |
| `UNIO_TEST_CA_FILE` | none | PEM CA certificate for an `https` endpoint. It is mounted into the container and used through `AWS_CA_BUNDLE`. |
| `S3TESTS_REF` | `5b3987116eca0b4b8fa57d6b10f3db57bab196b6` | ceph/s3-tests commit to build. Set another commit SHA to test a different revision. No tags existed on the repository when this pin was chosen, so a commit SHA is the only stable reference. |
| `S3TESTS_IMAGE` | none | Use this image instead of building one. It is pulled if it is not present locally, and it must already contain the suite. |
| `S3TESTS_TARGET` | `0.90` | Minimum subset pass rate, from 0 to 1. |
| `S3TESTS_RESULTS_DIR` | a new `mktemp -d` directory | Where `junit.xml` is written. The path is printed. |

The configuration is written to a directory created with `mktemp -d` and
removed when the script exits. It is never written into the repository. The
upstream loader requires the `[s3 alt]`, `[s3 tenant]`, `[iam]`, `[iam root]`
and `[iam alt root]` sections, so they repeat the main credentials. The tests
that need a second account, a tenant or IAM are not in the subset.

## Pass rate

The summarizer reads `junit.xml`, maps each subset node ID to its test name, and
classifies it:

- passed: the test ran and has no failure, error or skip element;
- failed: the test has a failure or error element;
- skipped: the test skipped itself, for example because the configuration does
  not provide something it needs;
- not run: the node ID has no entry in the report.

Two rates are printed:

- **Pass rate over the subset** is passed divided by the number of subset tests.
  Skipped and not-run tests count as not passed. This is the figure the target
  applies to.
- **Pass rate over executed tests** is passed divided by passed plus failed. It
  shows how the implementation does on the tests that actually ran.

## The subset

`subset.txt` lists 203 tests from `s3tests/functional/test_s3.py` at the pinned
commit. It was derived as follows:

1. Keep test functions whose names start with the v0.1 areas: `bucket_create`,
   `bucket_head`, `bucket_list`, `bucketv2` and `listv2`, `bucket_delete`,
   `bucket_get_location`, bucket naming, the `multi_object*_delete` family,
   `object_write`, `object_read`, `object_head`, `object_set_get`,
   `object_metadata`, `object_headers`, `object_copy`, `object_raw` (presigned
   and authenticated requests), `object_checksum`, `object_presigned`, the
   `get_object_if*` and `copy_object_if*` conditionals, `multipart_*`,
   `abort_multipart`, `list_multipart`, `upload_part_copy`, `ranged_*` and the
   `atomic_*` large-object tests.
2. Remove names that belong to excluded areas: anonymous access, ACLs, bucket
   policy, versioning and versioned copies, server-side encryption, object-lock,
   conditional writes (`put_object_if*`, `delete_object*_if*`, `*_current_*`),
   GetObjectAttributes, GET with `partNumber`, torrent, and multipart checksum
   helpers that need composite checksums.
3. Remove every test that uses `get_alt_client`, `get_tenant_client` or another
   account, since the suite has only one account.

The excluded areas listed in the header of `subset.txt` are the ones in the
v0.1 exclusion list: versioning, lifecycle, policy, ACL, SSE, object lock,
website, CORS, tagging beyond v0.1, replication, and the other areas named in
architecture section 9 as later milestones.

## Known limits of the setup

- Tests skipped by the configuration count as not passed. Only
  `test_bucket_get_location` depends on the configuration, through `api_name`,
  and that is empty for `us-east-1`.
- The trailing-checksum test in the Go suite needs HTTPS. This runner does not
  change that. Run it with an `https` endpoint and `UNIO_TEST_CA_FILE` if that
  case matters for the run.
- Upstream assertions that are not pinned by any public document are reported as
  they are. A failure there is a question for the team, not a fixed fact.
