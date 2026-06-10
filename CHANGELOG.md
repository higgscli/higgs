# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial public release of `protoncli`.
- 29 subcommands across read, write, LLM, and agentic workflows:
  - Read: `scan-folders`, `search`, `fetch-and-parse`, `attachments`,
    `threads`, `thread`, `state` (stats/clear), `schema`.
  - Write: `move`, `flag`, `mark-read`, `archive`, `trash`, `apply-labels`,
    `cleanup-labels`, `draft`, `send`, `unsubscribe`, `import`, `export`.
  - LLM: `classify` (with `--min-confidence`), `summarize`, `digest`,
    `extract` (schema-driven), `ask` (agentic Q&A).
  - Ops: `backfill`, `watch` (polling), `auth` (login/logout/status).
- `search` supports IMAP SEARCH criteria (`--from/--to/--subject/--body/
  --since/--before/--larger/--smaller/--seen/--unseen/--flagged/--keyword`).
- `attachments` extracts MIME parts with SHA-256, defensive filename
  sanitization, and filename-glob filtering.
- `summarize/digest/extract` use Ollama structured outputs. `extract` ships
  with embedded preset schemas (invoice, shipping, meeting) plus `--schema FILE`.
- `ask` runs a plan→invoke→observe loop over a whitelisted read-only tool
  subset discovered via `schema`. Write-capable subcommands are excluded.
- `watch` streams `new`/`expunge` events as NDJSON via polling-based diffing
  (no IMAP IDLE dependency).
- `export`/`import` roundtrip mailboxes via mbox (mboxrd) or JSONL, with
  optional gzip.
- `draft` composes RFC5322 messages and APPENDs to the Drafts mailbox — does
  NOT send. `unsubscribe` honors `List-Unsubscribe` / `List-Unsubscribe-Post`
  (one-click POST or GET; `mailto:` via SMTP when configured).
- `send` composes and delivers a message over SMTP (`PM_SMTP_*`), sharing the
  `draft` flag set. Supports `--in-reply-to` for threaded replies (e.g.
  replying to a meeting invite), `--dry-run` previews, and an optional
  `--save-to-sent` (off by default since Proton Bridge auto-files Sent). SMTP
  delivery failures surface as `api` (exit 1) with `reason: "smtpError"`.
- Credential storage via OS keyring (macOS Keychain / Windows Credential
  Manager / libsecret) with an encrypted-file fallback
  (`~/.protoncli/credentials.enc`, AES-256-GCM keyed by Argon2id from
  `PM_KEYSTORE_PASSPHRASE`), and `auth login/logout/status` subcommands.
  `PM_IMAP_USERNAME`/`PM_IMAP_PASSWORD` still work and take precedence.
- Typed error envelope with stable, documented exit codes:
  0 success, 1 api, 2 auth, 3 validation, 4 config, 5 imap, 6 classify,
  7 state, 8 discovery, 9 internal.
- NDJSON output contract: every streaming command ends with
  `{"type":"summary", ...}`; per-row dry-runs use `{"type":"pending", ...}`.
- Terminal-output sanitization (ANSI / bidi / zero-width / separator stripping).
- Embedded label taxonomy (11 canonical labels, 612 aliases) loaded from
  `internal/labels/data/labels.toml`.
- In-memory IMAP backend for integration tests (`internal/imaptest`).
- Pure-Go SQLite driver (`modernc.org/sqlite`) for `CGO_ENABLED=0` cross-builds.
- `--version` flag with build-time ldflags injection.
- Makefile targets: `build`, `test`, `test-race`, `cover`, `cover-html`,
  `vet`, `vuln`, `check`, `clean`, `tidy`.

[Unreleased]: https://github.com/akeemjenkins/protoncli/commits/main
