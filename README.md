# codex-tg

`codex-tg` is a Windows-first Go bridge between a local Codex TUI session and
an authorized Telegram private chat. Its goal is to let one operator continue
the same Codex thread from either interface without exposing Codex App Server
to the network.

> [!WARNING]
> This project is in early development. It is not ready for production use,
> and the current bootstrap may not build while core packages are being added.

## Intended architecture

One `codex-tg serve` process will:

1. Start `codex app-server` on an authenticated loopback WebSocket.
2. Poll Telegram for messages from one configured user and private chat.
3. Coordinate projects, Codex threads, turns, and approvals.
4. Persist non-secret state in SQLite.

`codex-tg open <path>` will ask the service to start or resume a thread and
then launch the local Codex TUI on that exact thread.

## Security model

The planned implementation follows these constraints:

- App Server and local control endpoints bind only to `127.0.0.1`.
- Telegram updates must match both the configured user ID and private chat ID.
- The Telegram bot token is stored in Windows Credential Manager, never in the
  repository, configuration file, database, logs, or process arguments.
- Codex may operate only inside explicitly allow-listed canonical project
  paths.
- Threads use the `workspace-write` sandbox and `on-request` approvals.
- Commit, push, migration, destructive, credential, and production-like
  operations require explicit approval.
- Tests must use temporary repositories, fakes, and test databases. They must
  never access a production database, Telegram chat, or service.

See [SECURITY.md](SECURITY.md) for vulnerability reporting.

## Requirements

- Windows 11 amd64 as the primary target.
- Go 1.26.4, as declared by `go.mod`.
- A compatible Codex CLI installation with App Server and remote thread
  support.
- [Task](https://taskfile.dev/) for development automation.
- `govulncheck` for dependency vulnerability checks.

Install the development tools:

```powershell
go install github.com/go-task/task/v3/cmd/task@latest
go install golang.org/x/vuln/cmd/govulncheck@latest
```

## Planned configuration

Non-secret configuration will use JSON similar to:

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

## Planned CLI

```text
codex-tg setup
codex-tg serve
codex-tg open [--new] <path>
codex-tg project add|list|remove
codex-tg status
codex-tg autostart install|remove|status
```

These commands document the intended interface; they are not all implemented
yet.

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

Until a release tag exists, builds are development snapshots identified by
their commit hash.

## License

Licensed under the [MIT License](LICENSE).
