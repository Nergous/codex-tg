# codex-tg

`codex-tg` is a Windows and Linux Go bridge between a local Codex TUI session and
an authorized Telegram private chat. Its goal is to let one operator continue
the same Codex thread from either interface without exposing Codex App Server
to the network.

Version `v0.1.1` provides the complete single-operator bridge lifecycle:
setup, supervised App Server startup, Telegram polling, shared threads, queued
prompts, approvals, recovery, local TUI attachment, and per-user autostart. It
is considered beta while the upstream Codex App Server protocol remains
experimental.

> [!WARNING]
> This project uses experimental Codex App Server protocol. Use it only with
> an allow-listed disposable or trusted local workspace.

## Architecture

One `codex-tg serve` process:

1. Start `codex app-server` on an authenticated loopback WebSocket.
2. Poll Telegram for messages from one configured user and private chat.
3. Coordinate projects, Codex threads, turns, and approvals.
4. Persist non-secret state in SQLite.

`codex-tg open <path>` asks the service to start or resume a thread and
then launch the local Codex TUI on that exact thread.

## Security model

The implementation enforces these constraints:

- App Server and local control endpoints bind only to `127.0.0.1`.
- Telegram updates must match both the configured user ID and private chat ID.
- The Telegram bot token is stored in Windows Credential Manager or the Linux
  Secret Service, never in the repository, configuration file, database, logs,
  or process arguments.
- Codex may operate only inside explicitly allow-listed canonical project
  paths.
- Threads use the `workspace-write` sandbox and `on-request` approvals.
- Commit, push, migration, destructive, credential, and production-like
  operations require explicit approval.
- Tests must use temporary repositories, fakes, and test databases. They must
  never access a production database, Telegram chat, or service.

See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## Requirements

- Windows 11 amd64 or Linux amd64 with a systemd user session.
- Go 1.26.4, as declared by `go.mod`.
- A compatible Codex CLI installation with App Server and remote thread
  support.
- [Task](https://taskfile.dev/) for development automation.
- `govulncheck` for dependency vulnerability checks.
- On Linux, `secret-tool` (`libsecret-tools` on Debian/Ubuntu) and an unlocked
  Secret Service-compatible keyring.

Install the development tools:

```powershell
go install github.com/go-task/task/v3/cmd/task@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
```

Install the Linux credential helper on Debian or Ubuntu:

```bash
sudo apt install libsecret-tools
```

The desktop session must provide an unlocked Secret Service-compatible
keyring. Linux autostart uses the current user's systemd manager; it never
installs a root service.

## Configuration

Non-secret configuration uses JSON similar to:

```json
{
  "telegram": {
    "allowed_user_id": 123456789,
    "allowed_chat_id": 123456789
  },
  "app_server": {
    "listen": "127.0.0.1:4500",
    "codex_binary": "C:\\Tools\\codex.exe"
  },
  "projects": [
    {
      "name": "example",
      "path": "D:\\Projects\\example"
    }
  ]
}
```

The Telegram bot token must not be placed in this file.

## CLI

```text
codex-tg setup
codex-tg serve
codex-tg open [--new] <path>
codex-tg project add|list|remove
codex-tg status
codex-tg autostart install|remove|status
```

Run `codex-tg setup` locally first. It validates the Telegram bot, stores its
token in the system credential store, and writes non-secret config under
`%LOCALAPPDATA%\codex-tg\config.json` on Windows or
`$XDG_CONFIG_HOME/codex-tg/config.json` on Linux (normally
`~/.config/codex-tg/config.json`).

Start the bridge with `codex-tg serve`, then use `codex-tg open [--new] <path>`
to attach the TUI to the exact service-owned thread. Use `project list`,
`project add <name> <path>`, and `project remove <name>` to maintain the
allow-list. `autostart install` creates a per-user Windows logon task or Linux
systemd user service.

Telegram commands: `/status`, `/projects`, `/project`, `/new`, `/resume`,
`/sessions`, `/diff`, `/cancel`, `/queue`, `/lock`, and `/unlock`.
`/sessions` lists recent persisted threads for the selected project. `/queue`
lists prompts waiting behind the active turn without consuming them.

To uninstall, run `codex-tg autostart remove`, stop the service, then remove
the platform config directory and the `codex-tg/telegram-bot-token` credential.

## Development

List available tasks:

```powershell
task --list
```

Common commands:

| Command | Purpose |
| --- | --- |
| `task fmt` | Format Go source files |
| `task fmt-check` | Fail when Go source is not formatted |
| `task tidy` | Synchronize `go.mod` and `go.sum` |
| `task test` | Run all tests once |
| `task test-race` | Run all tests with the race detector |
| `task coverage` | Generate text and HTML coverage reports |
| `task vet` | Run `go vet` |
| `task vuln` | Run `govulncheck` |
| `task build` | Build the Windows executable |
| `task build-linux` | Build the Linux amd64 executable |
| `task check` | Run formatting, vet, tests, and build checks |
| `task validate` | Run `check`, race tests, and vulnerability checks |
| `task version` | Print the version derived from Git |
| `task clean` | Remove generated `.artifacts` files |

Coverage output is stored outside the repository root files:

```text
.artifacts/coverage/coverage.out
.artifacts/coverage/coverage.html
```

## Versioning and releases

User-visible changes are collected under `Unreleased` in
[CHANGELOG.md](CHANGELOG.md). Release versions follow Semantic Versioning and
are represented by annotated Git tags such as `v0.1.0`.

Development builds use `git describe --tags --always --dirty`, so they remain
identifiable before the first release. A separate `VERSION` file is not used
because it would duplicate the Git tag and could become inconsistent.

Release flow:

1. Move relevant entries from `Unreleased` to a dated version section.
2. Run `task validate`.
3. Create a release commit.
4. Create and push an annotated version tag.
5. Let the release workflow build and publish artifacts.

Builds made between release tags are development snapshots identified by their
commit hash.

## License

Licensed under the [MIT License](LICENSE).
