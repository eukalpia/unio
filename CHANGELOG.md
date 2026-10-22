# Changelog

All notable changes to this project are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the
project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Architecture design and architecture decision records.
- Project governance, contribution guidelines and security policy.
- Configuration keys `node.region` (`UNIO_REGION`), `node.domains`
  (`UNIO_DOMAINS`), `metadata.dir` (`UNIO_METADATA_DIR`),
  `timeouts.write_idle` (`UNIO_WRITE_IDLE_TIMEOUT`) and
  `metrics.pprof_address` (`UNIO_PPROF_ADDRESS`).

### Changed

- Configuration is checked with the same rules the node applies at start.
  Node identity comes from the host of the node's endpoints, and two hosts
  that name the same node are refused. Root credentials are checked with
  `auth.ValidateRootCredentials`.
- Endpoints must use `https`. Internode traffic is always mutual TLS.
- `timeouts.write` defaults to no cap, so large downloads are not cut off.
- `metrics.token` is optional. Pprof requires it and listens only on a
  loopback address.
- `unio init` generates a root access key of letters and digits, which the
  node accepts.

### Removed

- `cluster.insecure_internode`, `UNIO_INSECURE_INTERNODE` and
  `--insecure-internode`. A configuration that still sets them fails to load
  and says that the setting was removed.
- `timeouts.read`, `UNIO_READ_TIMEOUT` and `--read-timeout`, replaced by
  `timeouts.read_idle`, `UNIO_READ_IDLE_TIMEOUT` and `--read-idle-timeout`.
  The read timeout bounds a stalled read, not a whole request.
