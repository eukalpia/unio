# Governance

## Roles

- **Contributors** are anyone who opens issues, reviews or submits changes.
- **Maintainers** ([MAINTAINERS.md](MAINTAINERS.md)) review and merge pull
  requests, triage issues, cut releases and own the architecture.

## Decision making

- Day-to-day changes are decided by maintainer review on pull requests.
- Architectural and contract changes are proposed as an
  [ADR](docs/adr/README.md) in a pull request. They are accepted by lazy
  consensus of maintainers after at least 3 business days of review, or
  sooner if every maintainer approves.
- If consensus cannot be reached, a simple majority of maintainers decides.

## Becoming a maintainer

Contributors with a sustained record of high-quality contributions and
reviews may be nominated by an existing maintainer. Nominations are
approved by a majority of maintainers.

## Releases

Maintainers cut `release/vX.Y` branches and tag releases following
[Semantic Versioning](https://semver.org). Each release has notes generated
from Conventional Commit history.
