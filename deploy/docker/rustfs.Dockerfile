# syntax=docker/dockerfile:1

# RustFS image for the comparison cluster (deploy/compose/rustfs-4x4.yml).
#
# The base image runs as the unprivileged rustfs user and owns /data. A named
# Docker volume copies the owner of an existing image directory, so the four
# drive mount points are created here, owned by rustfs. Without this, Docker
# creates them as root and RustFS fails with "Permission denied".
#
# The base is pinned by digest, the same image that deploy/compose/rustfs-4x4.yml
# resolved on 2026-10-22.

FROM rustfs/rustfs@sha256:9ca9559533b537d10482fe923288b676ad08f92a7069d9a17327fbae3cab688b

USER root
RUN mkdir -p /data1 /data2 /data3 /data4 \
 && chown rustfs:rustfs /data1 /data2 /data3 /data4
USER 10001:10001
