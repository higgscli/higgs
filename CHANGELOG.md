# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `imapfetch.FetchRFC822` no longer swallows mid-stream FETCH errors: a
  connection drop or server error during a fetch used to return the partial
  message list with no error, so every consumer (`extract`, `classify`,
  `summarize`, `digest`, `attachments`, `fetch-and-parse`, `unsubscribe`,
  reply composition) treated a truncated mailbox read as complete.
- Commands taking explicit `--uid` (`extract`, `unsubscribe`, `attachments`,
  `summarize`) now emit a `"type":"error"` row for each requested UID the
  server didn't return (e.g. a typo'd or already-deleted UID) and count it in
  the summary's `failed` — previously such UIDs vanished from the output
  entirely.
- `mark-read`/`flag` now verify the STORE per chunk of 250 UIDs (same
  discipline as the archive/trash/move fix): success rows are emitted only
  for UIDs confirmed in the requested flag state, wrong-state and nonexistent
  UIDs get error rows plus a `failed` count and a non-zero exit. Previously
  one unverified `UID STORE` OK produced a success row for every input UID.
- `cleanup-labels` no longer reports `"status":"ok"` for a label whose DELETE
  the server rejected; it now emits `"status":"failed"` with the error
  envelope (including the moved-messages count when messages had been
  consolidated) and counts it in `failed`.
- `watch` re-runs each poll's `UID SEARCH` until two consecutive answers
  agree, so a single flaky answer (Proton Bridge All Mail) no longer emits a
  burst of phantom `expunge`/`new` events.
- `apply-labels` surfaces state-DB write failures as warnings instead of
  discarding them (a lost `applied` record causes silent re-application on
  the next run), and `import`'s APPEND error now labels the message index
  correctly instead of calling it a UID.

## [1.0.5] - 2026-07-05

### Fixed

- `search`/`--all-matching` no longer trust a single `UID SEARCH` answer.
  Proton Bridge's virtual "All Mail" mailbox can return different results for
  the identical query while its view settles (observed: 122 matches, then 28
  on immediate re-run). Searches now re-run until two consecutive runs agree
  (up to 5 runs, warning on stderr when instability is detected).
- Error envelopes now include a `cause` field carrying the underlying error
  text when the message doesn't already contain it, and the stderr summary
  appends it. Previously `extract` failures all collapsed to
  `{"code":500,"kind":"classify","message":"extract","reason":"classifyError"}`
  regardless of the real Ollama parse error, and IMAP errors (e.g. `trash
  "All Mail"`) hid the server's actual rejection text. Applies to all wrapped
  errors: extract, summarize, digest, and IMAP operations.

- `archive`/`trash`/`move` no longer report success without verifying it.
  Previously a single MOVE was issued for the whole UID set and every UID was
  logged as `archived`/`trashed`/`moved` as long as the server answered OK —
  with large batches (10k+ UIDs) Proton Bridge acknowledges the command while
  applying it only partially, silently leaving messages behind. Moves now run
  in chunks of 250 UIDs, each chunk is verified with `UID SEARCH` against the
  source mailbox, stragglers are retried once, and UIDs still present are
  emitted as `"type":"error"` rows with a `failed` count in the summary and a
  non-zero exit code.
- The COPY+STORE+EXPUNGE fallback after a failed `UID MOVE` now narrows to the
  UIDs still present in the source, so a partially applied MOVE can no longer
  produce duplicate messages in the destination.

## [1.0.0] - 2026-04-19

### Added

- Initial public release of `higgs`.
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
  (`~/.higgs/credentials.enc`, AES-256-GCM keyed by Argon2id from
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

[Unreleased]: https://github.com/higgscli/higgs/commits/main
