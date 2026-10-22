# Contributing to Unio

Thank you for your interest in Unio. This guide explains how we work. Please
read it before opening a pull request.

## Ground rules

- Be respectful. All interactions follow the [Code of Conduct](CODE_OF_CONDUCT.md).
- Priorities are **security and integrity → stability → performance**, in
  that order. A change that makes something faster but weakens an invariant
  from the [architecture](docs/design/architecture.md#12-correctness-invariants)
  will not be merged.
- Discuss significant changes in an issue first. Changes to contracts
  (`internal/core`, `api/proto`) or to the architecture require an
  [ADR](docs/adr/README.md).
- Report security vulnerabilities privately. See [SECURITY.md](SECURITY.md).

## Development environment

| Tool | Version |
|---|---|
| Go | 1.25 or newer |
| OS | Linux or macOS. On Windows, use **WSL2** (Ubuntu) |
| C compiler | gcc or clang, only for the race detector (`-race` needs cgo) |
| Docker | For cluster, integration and benchmark tests |

Install the linters once:

```sh
go install honnef.co/go/tools/cmd/staticcheck@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
go install github.com/securego/gosec/v2/cmd/gosec@latest
```

## Workflow

Unio uses **trunk-based development**. `main` is always releasable and
protected. Every change lands through a pull request that passes CI and
review.

1. Find or open an issue. Comment that you are working on it.
2. Create a branch from an up-to-date `main`.
3. Make focused commits with tests.
4. Open a pull request that references the issue (`Closes #123`).
5. Address review feedback. A maintainer squash-merges the PR once it is
   approved and green.

### Branch names

```
<type>/<scope>-<short-description>
```

| Type | Use for |
|---|---|
| `feat` | New functionality |
| `fix` | Bug fixes |
| `perf` | Performance improvements |
| `refactor` | Restructuring without behaviour change |
| `test` | Tests only |
| `docs` | Documentation only |
| `build` / `ci` | Build system, CI |
| `chore` | Maintenance |

Examples: `feat/storage-group-commit`, `fix/auth-chunked-trailer`,
`perf/erasure-pipelined-encode`.

Release branches are named `release/vX.Y`, and releases are tagged `vX.Y.Z`
following [Semantic Versioning](https://semver.org). Fixes are merged to
`main` first and then cherry-picked into release branches.

### Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org). The
scope is the package or area:

```
feat(storage): add group commit for segment appends

Concurrent appends to the same segment now share a single fdatasync,
which raises small-object PUT throughput without weakening durability.

Closes #42
```

- The subject is imperative, lower case, and at most 72 characters.
- The body explains *why*, not just *what*.
- Mark breaking changes with `!` (`feat(core)!: ...`) and a
  `BREAKING CHANGE:` footer.

### Developer Certificate of Origin

Every commit must be signed off to certify the
[Developer Certificate of Origin](https://developercertificate.org):

```sh
git commit -s
```

This adds a `Signed-off-by: Your Name <you@example.com>` trailer. Pull
requests with commits that are not signed off fail CI.

## Code standards

- `gofmt` and `goimports` formatting; `go vet` and `staticcheck` clean.
- Pass `context.Context` as the first argument of every function that does
  I/O or may block, and honour cancellation.
- Wrap errors with `%w`, and use the typed errors from `internal/core` at
  module boundaries. Never expose internal error details to S3 clients.
- Library code never panics. There is no global mutable state.
- **Bounded resources.** Every queue, pool and goroutine fan-out has a limit.
  Buffers come from `internal/bufpool`.
- **Hot paths** avoid allocations, reflection and needless copies, and have
  benchmarks to prove it.
- Log with `log/slog`. Never log secrets, signatures or object data.
- Document every exported identifier.
- **Module boundaries.** A package depends on other modules only through
  interfaces in `internal/core`, never on their internals.

## Testing requirements

A pull request is ready for review only when all of these pass locally:

```sh
go build ./...
go vet ./...
staticcheck ./...
go test -race -count=1 ./...
govulncheck ./...
```

What we expect from tests:

- **Unit tests** for new behaviour. We practise test-driven development:
  write the failing test first.
- **Conformance suites.** Implementations of `internal/core` interfaces must
  pass the matching suite in `internal/core/coretest`, for example
  `coretest.RunDriveSuite`.
- **Fuzz tests** for every parser of untrusted input: headers, signatures,
  XML, wire frames, on-disk records.
- **Benchmarks** for hot paths. Include `benchstat` output in the PR when
  you claim a performance change.
- **`goleak`** in packages that start goroutines.
- **No `time.Sleep` for synchronisation** in tests. Use channels,
  conditions or fake clocks.
- **Tests for invariants I1–I5** whenever a change touches the write, read
  or delete paths.

Integration and chaos tests live under `test/` and run against a Docker
Compose cluster.

## Pull request checklist

The [PR template](.github/PULL_REQUEST_TEMPLATE.md) contains the checklist:
tests, docs, ADR for contract changes, benchmarks for performance claims,
and DCO sign-off. Keep PRs focused. Several small PRs are reviewed much
faster than one large one.

## Code ownership

Areas and their owners are listed in [.github/CODEOWNERS](.github/CODEOWNERS).
Changes to `internal/core`, `api/proto`, `go.mod` and security-sensitive
packages (`auth`, `cluster`) always require a maintainer review.

## License

By contributing, you agree that your contributions are licensed under the
[Apache License 2.0](LICENSE).
