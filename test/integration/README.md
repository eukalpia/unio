# Integration suite

This package is a black-box test suite for S3-compatible endpoints. It talks to
a running endpoint through the AWS SDK for Go v2, with path-style addressing,
and reads its configuration only from the environment. It has no dependency on
Unio's internal packages, so the same tests run against MinIO, Unio, or any
other endpoint that speaks the S3 API.

## Configuration

| Variable | Required | Meaning |
|---|---|---|
| `UNIO_TEST_ENDPOINT` | Yes, to run the tests | Base URL, for example `http://localhost:9001`. Scheme, host and port only: no path, query or embedded credentials. When it is unset, every endpoint test is skipped with a message. |
| `UNIO_TEST_ACCESS_KEY` | With the endpoint | Access key ID. |
| `UNIO_TEST_SECRET_KEY` | With the endpoint | Secret access key. |
| `UNIO_TEST_REGION` | No | Signing region. Default `us-east-1`. |
| `UNIO_TEST_CA_FILE` | No | PEM file with the CA certificate for an `https` endpoint whose CA is not in the system pool. Rejected for `http` endpoints. |

Each test creates its own bucket named `unio-it-` followed by 12 random
lowercase hex characters, and removes it in `t.Cleanup`, together with every
object and multipart upload in it. A leaked bucket fails the run.

The parsing of these variables is covered by a table test
(`TestParseConfig`) that runs even when the endpoint is unset.

## Run

```sh
# Without an endpoint: the configuration tests run, the endpoint tests skip.
go test -count=1 ./test/integration/...

# Against an endpoint.
export UNIO_TEST_ENDPOINT=http://localhost:9001
export UNIO_TEST_ACCESS_KEY=...
export UNIO_TEST_SECRET_KEY=...
go test -count=1 -v ./test/integration/...

# Skip the 100 MiB multipart test.
go test -count=1 -short -v ./test/integration/...
```

In PowerShell, use `$env:UNIO_TEST_ENDPOINT = "http://localhost:9001"` and the
same form for the other variables.

## Validate against a reference server first

Run the suite against a reference S3 server before using it on Unio, so that a
failure points at the test or at Unio and not at an assumption in the suite.
The reference used here is RustFS (`rustfs/rustfs`). Docker Hub and quay.io
refused every `minio/minio` pull when this was written, so MinIO could not be
used. Start RustFS with throwaway credentials: an access key of at
least 12 characters and a secret of at least 32 characters.

```sh
docker run -d --rm --name unio-it-s3 -p 19000:9000 \
  -e RUSTFS_ACCESS_KEY=... -e RUSTFS_SECRET_KEY=... rustfs/rustfs:latest
```

The image listens on port 9000 and stores data in `/data` (its default
`RUSTFS_VOLUMES`). `curl http://localhost:19000/health` returns 200 once it is
ready, and an unauthenticated `GET /` returns 403, which is the expected S3
answer.

Then point the suite at it:

```sh
export UNIO_TEST_ENDPOINT=http://localhost:19000
export UNIO_TEST_ACCESS_KEY=...    # the RUSTFS_ACCESS_KEY value
export UNIO_TEST_SECRET_KEY=...    # the RUSTFS_SECRET_KEY value
go test -count=1 -v ./test/integration/...
```

Stop the container when you are done: `docker rm -f unio-it-s3`.

### Results against RustFS (latest, 2026-10-22)

154 passed, 7 failed, 1 skipped. Every failure is a behaviour that RustFS does
not implement and that the S3 API requires, so the test is correct and the
server is the gap:

| Test | Expected by S3 | RustFS |
|---|---|---|
| `TestDeleteObjectsQuiet` | quiet mode lists only errors, no `Deleted` entries | returns 3 `Deleted` entries |
| `TestObjectHTTPHeadersRoundTrip` | `Expires` is returned on GET | the header is dropped |
| `TestObjectKeyLength/1024_*` | a 1024-byte key is accepted | `InvalidArgument` |
| `TestObjectUserMetadataLimit/2100_bytes` | `400 MetadataTooLarge` | accepted |

The skipped test, `TestChecksumDefaultTrailerStreamingUpload`, needs https:
the SDK refuses an unseekable body with a trailing checksum over plain HTTP.
Run the suite once against an https endpoint to cover it.

An earlier run against SeaweedFS (a stand-in, not a reference) gave 143
passed, 18 failed and 1 skipped. Its failures were the same kinds of gap, plus
bucket-deletion and missing-bucket errors.
