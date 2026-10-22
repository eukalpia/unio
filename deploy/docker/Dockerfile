# Unio server image.
#
# Build from the repository root:
#   docker build -f deploy/docker/Dockerfile -t unio:local .
#
# Build arguments end up in image metadata and build logs. Never pass
# credentials as build arguments; provide secrets at runtime instead.

ARG GO_IMAGE=golang:1.27.3-bookworm

# The web console is optional. By default no Node stage runs, and the image embeds
# the placeholder page. Build the console into the image with:
#   docker build -f deploy/docker/Dockerfile --build-arg CONSOLE_STAGE=console-build -t unio:local .
# That needs NODE_IMAGE to be available. Node is not in the base image, and it
# is not cached on every build machine.
ARG NODE_IMAGE=node:22-bookworm-slim
ARG CONSOLE_STAGE=console-skip

# ---------------------------------------------------------------------------
# Console build (only when CONSOLE_STAGE=console-build)
# ---------------------------------------------------------------------------
FROM ${NODE_IMAGE} AS console-build

WORKDIR /src

# Only the sources are copied. A host node_modules may hold binaries for another platform.
COPY web/console/package.json web/console/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/console/index.html web/console/vite.config.ts web/console/tsconfig.json web/console/tsconfig.node.json web/console/eslint.config.js ./
COPY web/console/public ./public
COPY web/console/scripts ./scripts
COPY web/console/src ./src
RUN npm run build && mkdir -p /console && mv dist /console/dist

# ---------------------------------------------------------------------------
# Console placeholder (the default). It leaves /console/dist empty, so the
# builder keeps the committed placeholder page.
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS console-skip
RUN mkdir -p /console/dist

# ---------------------------------------------------------------------------
# The stage the builder copies from. It is one of the two stages above.
# ---------------------------------------------------------------------------
FROM ${CONSOLE_STAGE} AS console

# ---------------------------------------------------------------------------
# Builder
# ---------------------------------------------------------------------------
FROM ${GO_IMAGE} AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

WORKDIR /src

# Modules first, so source-only changes reuse this layer.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    GOTOOLCHAIN=local go mod download

COPY . .

# Embed the built console. With the default CONSOLE_STAGE this copies nothing,
# and internal/console keeps its committed placeholder page.
COPY --from=console /console/dist/ /src/internal/console/assets/

# Drive mount points for the volumes in deploy/compose/cluster-4x4.yml and
# deploy/compose/single.yml.
# A named volume copies the owner of an existing image directory; without
# these, Docker creates the mount point as root and the nonroot process cannot
# write to it. They are copied in the runtime stage as the contents of
# /out/drives, so the directories end up at /data1 to /data4.
RUN mkdir -p /out/drives/data1 /out/drives/data2 /out/drives/data3 /out/drives/data4

# -X targets linker symbols of package main, and cmd/unio is package main, so
# the symbol path is main.<name>. A full import path would be silently ignored.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOTOOLCHAIN=local CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/unio ./cmd/unio

# ---------------------------------------------------------------------------
# Runtime
# ---------------------------------------------------------------------------
# Distroless: no shell, no package manager, no libc. Only the binary and the
# drive mount points are added.
FROM gcr.io/distroless/static-debian12:nonroot

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="Unio" \
      org.opencontainers.image.description="Distributed S3-compatible object store" \
      org.opencontainers.image.source="https://github.com/eukalpia/unio" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}"

# The binary stays root-owned and read-only for the nonroot user.
COPY --from=builder /out/unio /usr/local/bin/unio
# Copies the contents of /out/drives (data1 to data4) into /, owned by nonroot.
COPY --from=builder --chown=65532:65532 /out/drives/ /

# S3 (9000) and internode UniWire (9100). Publish S3 only; 9100 stays internal.
# Readiness is served on the S3 port at /unio/health/ready, and liveness at
# /unio/health/live.
EXPOSE 9000 9100

# Numeric UID so that Kubernetes runAsNonRoot can verify the user.
USER 65532:65532

ENTRYPOINT ["/usr/local/bin/unio"]
CMD ["server"]

# The probe asks for readiness, so the container is healthy only when the node
# can serve requests. The probe runs in the container, against the S3 port.
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/unio", "healthcheck", "--url", "http://127.0.0.1:9000/unio/health/ready"]
