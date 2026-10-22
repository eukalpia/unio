# MinIO image for the comparison cluster (deploy/compose/minio-4x4.yml).
#
# MinIO no longer publishes community images or binaries, and its repository
# is archived, so the last release is built from source here. MinIO is
# AGPL-3.0; this file only builds it for local comparison runs, and no image
# built from it is published. The bases are the ones the Unio image uses.

FROM golang:1.27.3-bookworm AS build
ARG MINIO_TAG=RELEASE.2025-10-15T17-29-55Z
RUN git clone --depth 1 --branch "${MINIO_TAG}" https://github.com/minio/minio /src
WORKDIR /src
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/minio . \
 && mkdir -p /out/drives/data1 /out/drives/data2 /out/drives/data3 /out/drives/data4

# Named volumes copy the owner of the image directory they mount over, so the
# drive mount points are created owned by the nonroot user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=65532:65532 /out/drives/ /
COPY --from=build /out/minio /usr/local/bin/minio
ENTRYPOINT ["/usr/local/bin/minio"]
