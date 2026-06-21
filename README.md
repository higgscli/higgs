# higgs

An agent-first CLI for Proton Mail. Schema manifest for tool use, NDJSON on stdout, typed error envelopes, and a stable exit-code enum — designed to be driven by a language model, not a human.

[![CI](https://github.com/higgscli/higgs/actions/workflows/ci.yml/badge.svg)](https://github.com/higgscli/higgs/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/higgscli/higgs?label=release&logo=github)](https://github.com/higgscli/higgs/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/higgscli/higgs.svg)](https://pkg.go.dev/github.com/higgscli/higgs)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-blue?logo=go)](https://go.dev/)

> **Unofficial project.** `higgs` is an independent, community-built CLI for Proton Mail. It is not affiliated with, endorsed by, or sponsored by Proton AG. "Proton", "Proton Mail", and related marks are trademarks of Proton AG; this project uses them only to describe interoperability.

 
## Why this exists
 
Wiring a normal CLI into an agent loop is painful. Stdout mixes prose and data, errors are English sentences, exit codes are 0-or-1, and the only tool specification is `--help`. `higgs` inverts that. Every design decision assumes the primary caller is a model:
 
- **Schema manifest.** `higgs schema` emits a JSON description of every subcommand — flags, args, stdout format, exit codes. An agent loads it once and can drive the tool without prompt-engineered command syntax.
- **NDJSON streaming with a terminator.** Every streaming command emits one JSON object per line and ends with `{"type":"summary", ...}`. Callers know when a stream is done without heuristics.
- **Typed error envelopes.** Every failure emits `{"error": {"kind", "code", "reason", "message", "hint"}}`. Agents branch on `.error.kind`, not on parsed English.
- **Exit codes as an enum.** Exit codes map 1:1 to error kinds, so retry and escalation are deterministic: retry on `5 imap`, prompt the user on `2 auth`, surface to the caller on `4 config`.
- **Sanitized stderr.** Human-readable progress on stderr, stripped of ANSI escapes, bidi controls, and zero-width characters — safe to feed back into a model's context.
- **Checkpointed state.** SQLite state DB with `backfill` and `state clear` so runs are resumable across crashes and restarts.
- **Secrets out-of-band.** Credentials go to the OS keyring (macOS Keychain, Windows Credential Manager, libsecret on Linux) with an AES-256-GCM file fallback, so nothing sensitive flows through an agent's context.
The first workload riding this contract is a local-only Proton Mail inbox classifier via Proton Mail Bridge and Ollama. The classifier is useful on its own, but the contract is the point.
 
## The classifier
 
`higgs classify` connects to a running [Proton Mail Bridge](https://proton.me/mail/bridge) over IMAP, streams each message through a local [Ollama](https://ollama.com/) model, and applies one or more labels from an 11-category taxonomy. Every step runs on `localhost`: no API keys, no cloud inference, no telemetry.
 
The default model is [Gemma 4](https://ollama.com/library/gemma4), chosen because it has native function-calling support, a 128K context window on the small variants, and fits comfortably on a laptop.
 
## Quickstart
 
1. Install and sign into [Proton Mail Bridge](https://proton.me/mail/bridge). Note the IMAP username and bridge password it assigns.
2. Install [Ollama](https://ollama.com/download) and pull a model:
   ```
   ollama pull gemma4
   ```
3. Install `higgs`:
   ```
   # Release tarball
   curl -L https://github.com/higgscli/higgs/releases/latest/download/higgs_1.0.2_darwin_arm64.tar.gz | tar xz
 
   # go install
   go install github.com/higgscli/higgs/cmd/higgs@latest
   ```
 
   Or build from source:
   ```
   git clone https://github.com/higgscli/higgs.git
   cd higgs
   make build
   ```
4. Export Bridge and Ollama settings. A `.env` at the repo root works:
   ```
   export PM_IMAP_USERNAME="alice@proton.me"
   export PM_IMAP_PASSWORD="bridge-generated-password"
   export PM_IMAP_HOST="127.0.0.1"
   export PM_IMAP_PORT="1143"
   export PM_OLLAMA_MODEL="gemma4"
   ```
5. Dry-run against your inbox:
   ```
   higgs classify --dry-run --limit 20 INBOX
   ```
 
   Review the NDJSON. When the suggestions look right, rerun with `--apply` to write labels back to Proton.
## The agent contract
 
### Schema manifest
 
`higgs schema` returns a manifest of every subcommand. Load it once, drive the CLI from it.
 
```
higgs schema classify
```
 
```
{
  "name": "classify",
  "summary": "Classify messages with Ollama and optionally apply labels",
  "args": [{"name": "mailbox", "required": false, "default": "INBOX"}],
  "flags": [
    {"name": "dry-run", "type": "bool", "description": "Preview suggestions without writing labels"},
    {"name": "apply", "type": "bool", "description": "Apply suggested labels to IMAP"},
    {"name": "limit", "type": "int", "default": 100},
    {"name": "workers", "type": "int", "default": 4},
    {"name": "no-state", "type": "bool"},
    {"name": "reprocess", "type": "bool"}
  ],
  "stdout": "ndjson",
  "exit_codes": [0, 2, 3, 4, 5, 6, 7, 9]
}
```
 
### Output contract
 
- **stdout**: structured JSON. Single-object commands pretty-print; streaming commands emit NDJSON.
- **stderr**: human-readable progress, sanitized of ANSI escapes, bidi controls, and zero-width characters.
- **NDJSON terminator**: every streaming command ends with one `{"type":"summary", ...}` line. Read until you see it.
- **Error envelope**: failures emit a typed envelope with `kind`, `code`, `reason`, `message`, and `hint`.
```
{
  "error": {
    "kind": "config",
    "code": 400,
    "reason": "configError",
    "message": "PM_IMAP_USERNAME is required",
    "hint": "export PM_IMAP_USERNAME=<bridge-username>"
  }
}
```
 
### Exit codes
 
| Code | Kind | Description |
| --- | --- | --- |
| 0 | success | Command completed without error |
| 1 | api | Upstream API error (generic) |
| 2 | auth | Authentication or credential failure |
| 3 | validation | Invalid flags, arguments, or input |
| 4 | config | Missing or malformed configuration |
| 5 | imap | IMAP protocol or connection error |
| 6 | classify | Classification error (Ollama, prompt, parsing) |
| 7 | state | State DB error (SQLite) |
| 8 | discovery | Mailbox discovery failure |
| 9 | internal | Unexpected internal error |
 
## Commands
 
The intended flow is: discover mailboxes, classify them, apply labels. Everything else (`backfill`, `cleanup-labels`, `state`) exists to repair or inspect state along the way.
 
### scan-folders
 
Enumerate IMAP mailboxes and return the canonical `All Mail` and `Labels` roots.
 
```
higgs scan-folders
```
 
```
{
  "mailboxes": [
    {"name": "INBOX", "delimiter": "/", "messages": 1204, "unseen": 18, "attributes": []},
    {"name": "Labels/Orders", "delimiter": "/", "attributes": ["\\HasNoChildren"]}
  ],
  "all_mail": "All Mail",
  "labels_root": "Labels"
}
```
 
### classify
 
Stream messages through Ollama and emit one NDJSON object per message, followed by a `summary` terminator. Add `--apply` to write labels back to IMAP in the same pass.
 
```
higgs classify --dry-run --limit 20 INBOX
higgs classify --apply --workers 4 "Folders/Accounts"
higgs classify --reprocess --no-state INBOX
```
 
Flags: `--dry-run`, `--apply`, `--limit N`, `--no-state`, `--reprocess`, `--workers N`.
 
```
{"mailbox":"INBOX","uid":1842,"uid_validity":1,"subject":"Your order has shipped","from":"ship-confirm@amazon.com","date":"2026-04-09T14:22:10Z","suggested_labels":["Orders"],"confidence":0.94,"rationale":"Shipping notification with tracking number","is_mailing_list":false}
{"type":"summary","mailbox":"INBOX","classified":20,"errors":0,"skipped":0}
```
 
### apply-labels
 
Apply pending labels recorded in the state DB. Use when `classify` ran without `--apply`.
 
```
higgs apply-labels --limit 100 "Folders/Accounts"
higgs apply-labels --dry-run "Folders/Accounts"
```
 
### cleanup-labels
 
Consolidate legacy or user-created labels into the canonical 11-label taxonomy. Useful after migrating from Proton's built-in filters.
 
```
higgs cleanup-labels --dry-run
higgs cleanup-labels
```
 
### fetch-and-parse
 
Fetch and parse messages without classifying. Useful for piping into other tools.
 
```
higgs fetch-and-parse INBOX | jq 'select(.from | contains("github"))'
```
 
### backfill
 
Replay a prior `classify` NDJSON log into the state DB. Recovers state after a crash or migration.
 
```
higgs backfill classify.log
```
 
### state
 
Inspect or reset the SQLite state DB.
 
```
higgs state stats
higgs state stats "Folders/Accounts"
higgs state clear "Folders/Accounts"
```
 
### schema
 
Emit a machine-readable manifest of every subcommand. See [The agent contract](#the-agent-contract).
 
```
higgs schema
higgs schema classify
```
 
## Configuration
 
All configuration is read from environment variables. Defaults target a standard Proton Mail Bridge + Ollama setup on the same host.
 
### Credentials
 
Credentials are stored in the OS keyring (macOS Keychain, Windows Credential Manager, Linux Secret Service via libsecret) so they never live in shell history or a `.env` file. When the keyring is unreachable, an encrypted-file fallback (`~/.higgs/credentials.enc`, AES-256-GCM with Argon2id-derived keys) is available.
 
```
# Prompt interactively and store via the OS keyring (default).
higgs auth login
 
# Pipe the password in from a secret manager:
pass show proton/bridge | higgs auth login --username alice@proton.me --password-stdin
 
# Check where credentials live and which backends are available.
higgs auth status
 
# Remove stored credentials from every backend.
higgs auth logout
```
 
If `PM_IMAP_USERNAME` and/or `PM_IMAP_PASSWORD` are set in the environment they always win — useful for one-off overrides in CI or shells. To use the encrypted-file backend, export `PM_KEYSTORE_PASSPHRASE` (required to read or write the file) and optionally `PM_KEYSTORE_PATH` to relocate it.
 
### IMAP (Proton Mail Bridge)
 
| Variable | Default | Description |
| --- | --- | --- |
| `PM_IMAP_HOST` | `127.0.0.1` | Bridge host |
| `PM_IMAP_PORT` | `1143` | Bridge IMAP port |
| `PM_IMAP_USERNAME` | *(required)* | Bridge IMAP username |
| `PM_IMAP_PASSWORD` | *(required)* | Bridge-generated password |
| `PM_IMAP_SECURITY` | `starttls` | One of `starttls`, `tls`, `insecure` |
| `PM_IMAP_TLS_SKIP_VERIFY` | auto | Skip TLS verification (auto-enabled for loopback) |
| `PM_IMAP_APPLY_TIMEOUT` | `180` | Per-command timeout (seconds) for `--apply` |
 
### Ollama
 
| Variable | Default | Description |
| --- | --- | --- |
| `PM_OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama API base URL |
| `PM_OLLAMA_MODEL` | `gemma4` | Model name passed to Ollama |
 
### Classify tuning
 
| Variable | Default | Description |
| --- | --- | --- |
| `PM_CLASSIFY_LIMIT` | `100` | Max messages per `classify` run |
| `PM_CLASSIFY_BATCH_SIZE` | `25` | IMAP fetch batch size |
| `PM_CLASSIFY_WORKERS` | `4` | Parallel Ollama workers |
 
### State
 
| Variable | Default | Description |
| --- | --- | --- |
| `PM_STATE_DB` | `~/.higgs/state.db` | SQLite state DB path |
 
## Label taxonomy
 
The classifier is constrained to 11 canonical labels. 612 aliases in `internal/labels/data/labels.toml` normalize legacy or model-generated names back to this set.
 
| Label | Covers |
| --- | --- |
| Orders | Purchase confirmations, shipping, returns |
| Finance | Banks, cards, taxes, invoices |
| Newsletters | Editorial digests, blog mailings |
| Promotions | Marketing, discounts, sales |
| Jobs | Recruiters, job boards, offers |
| Social | Social network notifications, friend activity |
| Services | SaaS account activity, product updates |
| Health | Providers, pharmacy, insurance |
| Travel | Flights, hotels, itineraries |
| Security | 2FA, password resets, security alerts |
| Signups | Account creation, email verification |
 
See [`internal/labels/data/labels.toml`](https://github.com/higgscli/higgs/blob/main/internal/labels/data/labels.toml) for the full alias map.
 
## Development
 
Every common task is wrapped in the repo `Makefile`.
 
| Target | Description |
| --- | --- |
| `make build` | Build the `./bin/higgs` binary |
| `make test` | Run `go test ./...` |
| `make test-race` | Run tests with the race detector |
| `make cover` | Coverage profile plus `go tool cover -func` summary |
| `make cover-html` | HTML coverage report at `coverage.html` |
| `make vet` | Run `go vet ./...` |
| `make vuln` | Run `govulncheck` (installs into `./bin` if missing) |
| `make check` | `vet` + `test-race` + `vuln` |
| `make clean` | Remove the built binary and coverage outputs |
| `make tidy` | Run `go mod tidy` |
 
Run `make check` before opening a PR.
 
## Contributing
 
Bug reports and pull requests are welcome — see [CONTRIBUTING.md](https://github.com/higgscli/higgs/blob/main/CONTRIBUTING.md) for the workflow and code-review expectations.
 
## Security
 
Please report vulnerabilities privately via the process in [SECURITY.md](https://github.com/higgscli/higgs/blob/main/SECURITY.md). Do not open public issues for security reports.
 
## License
 
Apache License 2.0 — see [LICENSE](https://github.com/higgscli/higgs/blob/main/LICENSE).
 
## Acknowledgements
 
- [Proton Mail Bridge](https://github.com/ProtonMail/proton-bridge) — local IMAP gateway to Proton Mail
- [Ollama](https://github.com/ollama/ollama) — local LLM runtime
- [emersion/go-imap](https://github.com/emersion/go-imap) — IMAP client library
- [spf13/cobra](https://github.com/spf13/cobra) — CLI framework
- [BurntSushi/toml](https://github.com/BurntSushi/toml) — TOML decoder for the label taxonomy
 