# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-07-28

### Added

- Linux amd64 support with Secret Service credential storage and systemd user
  autostart.
- Windows-first Go CLI skeleton for the Codex Telegram bridge.
- Strict JSON configuration loading and validation for Telegram access, the
  loopback Codex App Server, and allow-listed project directories.
- Telegram bot token storage through Windows Credential Manager.
- Development and release automation with Task and GoReleaser.
- Security policy, implementation plan, and project documentation.
- Service lifecycle, local setup, recovery state, and Windows autostart.
- Operator configuration runbook and fake bridge integration coverage.
