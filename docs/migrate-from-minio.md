# Moving from MinIO (or any S3 store) to Unio

`unio-mirror` copies buckets from an S3-compatible store to Unio and checks
the copy. It never deletes anything: objects that only the destination holds
are counted and left alone.

```sh
go build -o unio-mirror ./cmd/unio-mirror

export UNIO_MIRROR_FROM_ACCESS_KEY=...  UNIO_MIRROR_FROM_SECRET_KEY=...   # MinIO
export UNIO_MIRROR_TO_ACCESS_KEY=...    UNIO_MIRROR_TO_SECRET_KEY=...     # Unio

unio-mirror -from http://minio:9000 -to http://unio:9000            # every bucket
unio-mirror -from http://minio:9000 -to http://unio:9000 -bucket lms # one bucket
unio-mirror -from http://minio:9000 -to http://unio:9000 -verify     # compare only
```

Credentials come from the environment only, never from flags, so they do not
show up in `ps` or the shell history. Exit status: 0 when the destination
matches the source, 1 when objects failed or differ, 2 on a usage or
connection error.

## What is copied

- Buckets, created when missing.
- Every object with its Content-Type, Content-Disposition, Content-Encoding,
  Content-Language, Cache-Control and user metadata.
- The bucket policy, which Unio validates as strictly as any other: a policy
  with named principals or conditions is refused, and the run stops so you
  can decide.

## How the copy is checked

- When the source ETag is the MD5 of the object (anything uploaded in one
  request), it is sent as Content-MD5: Unio refuses the upload unless it
  received exactly those bytes.
- Multipart objects have no content hash in their ETag. They are checked by
  size and by the MD5 computed while copying, and the source ETag is kept in
  the user metadata key `unio-mirror-source-etag`, so a later run can tell an
  unchanged object from a changed one of the same size.
- After each write the object is read back with HEAD.

A second run copies only what is missing or changed, so the mirror can run
while the old store still serves traffic.

## A safe switch

1. Run `unio-mirror` until it reports nothing to copy.
2. Point the application at Unio for writes and at MinIO for reads, or stop
   writes for the few minutes of the last step.
3. Run `unio-mirror` once more, then `unio-mirror -verify`; it must exit 0.
4. Point reads at Unio. Keep MinIO, read-only, until you are confident;
   switching back is a configuration change.
