# Security Policy

## Supported versions

Security fixes target the latest release of the DSH plugin (`0.2.x`) and the
default branch. The archived Go bridge (`0.1.x`, tag `v0.1.5`)
is no longer supported.

## Reporting a vulnerability

Do not disclose suspected vulnerabilities in a public issue, discussion,
commit, or pull request.

Use GitHub private vulnerability reporting for this repository when it is
available. If it is unavailable, contact the repository owner through GitHub
without including exploit details and request a private communication channel.

Include the following information when possible:

- Affected version, Git tag, or commit hash.
- Operating system and DeepSeek Harness version.
- Reproduction steps or a minimal proof of concept.
- Expected and observed behavior.
- Potential impact and suggested mitigation.
- Whether the vulnerability has been disclosed elsewhere.

Never include real Telegram bot tokens, credentials, private chat contents, or
sensitive project data. Replace them with clearly marked test values.

## Security-sensitive areas

Reports are especially useful for:

- Bypasses of the bound group or allowed-user checks, including inline button
  callbacks.
- Bot token leakage through logs, errors, settings routes, storage, or
  Telegram messages.
- Settings routes reachable without DSH authentication.
- Approval or question answers accepted from the wrong session, user, or
  prompt.
- Telegram input reaching a session other than the one its topic is linked to.

## Disclosure

Allow time to investigate and prepare a fix before public disclosure. The
maintainer will coordinate disclosure when practical, but the project does not
currently provide a guaranteed response-time SLA.
