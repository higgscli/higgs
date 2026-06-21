# Contributing to higgs

Thanks for your interest in improving higgs. This document covers the dev
workflow, PR expectations, and the commit convention.

## Prerequisites

- Go 1.25 or newer
- `make`
- For running the tool locally: [Proton Mail Bridge](https://proton.me/mail/bridge)
  and [Ollama](https://ollama.com/) with a pulled model (e.g. `ollama pull gemma4`).

## Setting up

```sh
git clone https://github.com/higgscli/higgs.git
cd personal-proton
make build
make test
```

The binary lands at `bin/higgs`.

## Before you open a PR

Run the full local check suite:

```sh
make check      # go vet + go test -race + govulncheck
```

Also recommended:

```sh
gofmt -l -w .
make cover      # prints per-file coverage
```

A PR should:

- target a single logical change (new feature, bugfix, refactor — not a mix)
- include tests for any behavior change
- leave coverage at or above the current level
- pass `make check` locally

## Commit messages

This repo follows [Conventional Commits](https://www.conventionalcommits.org/).
GoReleaser builds release notes from the commit log, so the prefix matters:

| Prefix      | Use for                                |
|-------------|----------------------------------------|
| `feat:`     | new user-visible behavior              |
| `fix:`      | bug fix                                |
| `docs:`     | documentation only                     |
| `refactor:` | internal change, no user-visible diff  |
| `test:`     | test-only change                       |
| `ci:`       | GitHub Actions / release pipeline      |
| `chore:`    | everything else (deps, scripts, etc.)  |
| `deps:`     | dependency bump                        |

Breaking changes use `feat!:` or `fix!:` and include a `BREAKING CHANGE:` footer.

## Proposing a change

- **Bug**: open an issue using the bug template.
- **Feature**: open a [Discussion](https://github.com/higgscli/higgs/discussions)
  first so the design can be agreed before code is written. For small additions
  (a flag, a new schema field), an issue is fine.

## Code style

- Godoc comments on every exported identifier (one sentence).
- No inline comments explaining *what* the code does — descriptive names do that.
  Comments explain *why*: a non-obvious invariant, a workaround, a subtle constraint.
- Keep functions small. If a function gets past ~80 lines, split it.
- No dead code, no TODO comments without an issue link.

## Releases

Maintainers tag `vX.Y.Z` on `main`. The `release.yml` workflow runs GoReleaser,
which builds cross-platform archives, computes checksums, signs artifacts with
cosign keyless, and publishes a GitHub Release. No manual release steps.

Version numbers follow [Semantic Versioning](https://semver.org/):

- `MAJOR` — breaking output-format, exit-code, or CLI-flag changes
- `MINOR` — new backward-compatible commands or flags
- `PATCH` — bugfixes, internal refactors

Pre-1.0 the public contract is the `schema` subcommand output and the exit-code
table. Breaking changes to those bump `MINOR`; post-1.0 they bump `MAJOR`.

## Reporting security issues

Do **not** open a public issue. See [SECURITY.md](SECURITY.md).
