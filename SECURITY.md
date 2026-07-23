# Security Policy

## Supported versions

`codex-tg` has no production-ready release yet. Security fixes currently
target the default branch on a best-effort basis. After versioned releases
begin, only the latest release line will receive security updates unless a
release announcement states otherwise.

## Reporting a vulnerability

Do not disclose suspected vulnerabilities in a public issue, discussion,
commit, or pull request.

Use GitHub private vulnerability reporting for this repository when it is
available. If it is unavailable, contact the repository owner through GitHub
without including exploit details and request a private communication channel.

Include the following information when possible:

- Affected version, Git tag, or commit hash.
- Operating system and Codex CLI version.
- Reproduction steps or a minimal proof of concept.
- Expected and observed behavior.
- Potential impact and suggested mitigation.
- Whether the vulnerability has been disclosed elsewhere.

Never include real Telegram bot tokens, App Server capability tokens,
credentials, private chat contents, or sensitive project data. Replace them
with clearly marked test values.

## Security-sensitive areas

Reports are especially useful for:

- Telegram user or chat authorization bypasses.
- Exposure of App Server or local IPC beyond loopback.
- Secret leakage through logs, configuration, SQLite, Telegram, or process
  arguments.
- Project allow-list, path canonicalization, symlink, or junction escapes.
- Approval replay, forgery, expiry, or privilege-escalation defects.
- Sandbox bypasses or unintended production operations.
- Unsafe crash recovery or duplicate prompt execution.

## Disclosure

Allow time to investigate and prepare a fix before public disclosure. The
maintainer will coordinate disclosure when practical, but the project does not
currently provide a guaranteed response-time SLA.
