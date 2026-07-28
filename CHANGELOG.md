# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Add resumable first-run pairing, per-user command installation, autostart,
  confirmed current-project registration, safe service startup, and
  no-argument current-directory opening.

## [0.1.4] - 2026-07-28

### Added

- Show Telegram typing activity while Codex works and react to submitted
  prompts with processing, success, or failure status.

### Fixed

- Accept the current Codex App Server `thread/list` response shape while
  retaining compatibility with the legacy response.
- Queue burst App Server events without disconnecting the bridge while
  Telegram rendering catches up.
- Raise the App Server WebSocket message limit from the library default of
  32 KiB to 10 MiB for thread and turn payloads.
- Attach the Codex TUI to the current terminal instead of launching it with
  disconnected standard streams.
- Adopt threads created by the remote Codex TUI, persist their selected
  projects, and subscribe the Telegram bridge after the first turn creates the
  rollout.
- Replace stale persisted threads whose rollout no longer exists instead of
  failing `open` or service startup.
- Parse current nested Codex thread, turn, item, and delta events so Telegram
  receives TUI-originated responses.
- Avoid duplicating streamed assistant messages when Codex later emits the
  completed item.
- Report App Server WebSocket disconnects to the service instead of leaving a
  session indefinitely marked as running.
- Show App Server, IPC, project, and runtime details when `serve` starts, and
  include captured App Server diagnostics when the bridge exits unexpectedly.

### Documentation

- Document safe Windows path forms for Git Bash commands.

## [0.1.3] - 2026-07-28

### Fixed

- Accept boolean Telegram Bot API results for operations whose response body is
  intentionally ignored, allowing `setup` to complete after deleting a webhook.

## [0.1.2] - 2026-07-28

### Changed

- Publish platform executables directly instead of wrapping them with README,
  LICENSE, and CHANGELOG in ZIP archives.

## [0.1.1] - 2026-07-28

### Changed

- Updated project documentation and security policy to reflect the working
  beta application rather than its former skeleton stage.

### Fixed

- Made `/queue` list persisted FIFO prompts without consuming them.
- Made `/sessions` return recent persisted project sessions with its requested
  limit.
- Included the last locally opened project and thread in CLI status output.
- Rejected trailing JSON in configuration files.
- Added graceful `SIGTERM` handling for Linux services.
- Removed obsolete unwired-command scaffolding.

## [0.1.0] - 2026-07-28

### Added

- Linux amd64 support with Secret Service credential storage and systemd user
  autostart.
- Complete Go CLI bridge lifecycle for one authorized Telegram operator.
- Strict JSON configuration loading and validation for Telegram access, the
  loopback Codex App Server, and allow-listed project directories.
- Telegram bot token storage through Windows Credential Manager.
- Development and release automation with Task and GoReleaser.
- Security policy, implementation plan, and project documentation.
- Service lifecycle, local setup, recovery state, and Windows autostart.
- Operator configuration runbook and fake bridge integration coverage.
