# higgs

Agent-first, local-only CLI for Proton Mail (Go 1.26, Cobra). Privacy is the
brand: everything runs on localhost (Proton Mail Bridge + Ollama), no cloud
inference, no API keys, no telemetry. Unofficial project — not affiliated with
Proton AG; keep the trademark disclaimer intact wherever it appears.

Sibling repos in the [higgscli org](https://github.com/higgscli):
`website` (higgscli.com — Cloudflare Worker `higgscli`) and `homebrew-higgs`
(tap; `Formula/higgs.rb` is GoReleaser-generated — **never hand-edit it**).

## Layout

- `cmd/higgs/` — main entrypoint
- `internal/` — implementation
- `docs/` — extended docs
- CI (`.github/workflows/ci.yml`): tests on ubuntu/macos/windows + golangci-lint,
  govulncheck, CodeQL. Releases via GoReleaser: cosign-signed, SBOM included.

## Contract invariants

Breaking any of these is a breaking change:

- Structured JSON only on stdout; streaming commands emit NDJSON ending with
  `{"type":"summary",...}`.
- Failures emit typed error envelopes: `kind`/`code`/`reason`/`message`/`hint`.
- Exit codes 0–9 map 1:1 to error kinds.
- stderr is sanitized (no ANSI escapes, bidi controls, zero-width chars).
- Secrets go through the OS keyring / encrypted file only — never through a
  model's context.
- Every command is discoverable via `higgs schema` and, if it takes `--uid`,
  composable via `--uid -` (plain UIDs or NDJSON on stdin).

## Rules

- Development is TDD-first: write the failing test before the implementation.
- **After every push to main: check the README and update it in the same
  sitting.** Diff what you pushed against what the README claims — versions,
  commands, flags, exit codes, install steps, badges, examples. A push is not
  done until the README is verified current.
- When a release ships, update the `website` repo too (version strings,
  JSON-LD, llms-full.txt, sitemap — checklist in its README) and deploy it.
- The website must never describe flags or commands that `higgs schema`
  doesn't report.
- No GitHub PATs for automation — use a GitHub App with short-lived
  installation tokens (`actions/create-github-app-token`).
