# Security Policy

## Supported versions

Only the latest minor release receives security fixes.

| Version         | Supported |
|-----------------|-----------|
| latest minor    | yes       |
| everything else | no        |

## Reporting a vulnerability

**Do not open a public GitHub issue for security reports.**

Use GitHub's private vulnerability reporting:
<https://github.com/higgscli/higgs/security/advisories/new>

Please include:

- A description of the issue and the potential impact.
- Steps to reproduce, or a minimal proof-of-concept.
- The `higgs --version` you observed it on.
- Any mitigations you already know of.

### Response SLA

- Acknowledgement within **5 business days**.
- Triage and severity classification within **10 business days**.
- For confirmed issues, a fix timeline will be communicated in the advisory.

## Scope

In scope:

- The `higgs` binary itself (CLI surface, IMAP code paths, Ollama client,
  state DB handling, terminal output sanitization, error envelope).

Out of scope (report upstream):

- Proton Mail Bridge vulnerabilities — report at <https://proton.me/security/bug-bounty>.
- Ollama runtime vulnerabilities — report to the [Ollama project](https://github.com/ollama/ollama/security).
- Vulnerabilities in dependencies unless they are introduced by how higgs
  uses them.

## Coordinated disclosure

We prefer coordinated disclosure. We will credit reporters in the release notes
unless they ask to remain anonymous.

## PGP key

No PGP key is published yet. Use GitHub's private vulnerability reporting for
encrypted submission.
