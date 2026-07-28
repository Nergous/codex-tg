# Configuration and recovery

`codex-tg setup` stores the Telegram token only in Windows Credential Manager
under `codex-tg/telegram-bot-token`. It writes the following non-secret file:
`%LOCALAPPDATA%\codex-tg\config.json`.

```json
{
  "telegram": {"allowed_user_id": 1, "allowed_chat_id": 1},
  "app_server": {"listen": "127.0.0.1:4500", "codex_binary": "C:\\Tools\\codex.exe"},
  "projects": [{"name": "work", "path": "D:\\Projects\\work"}]
}
```

All listeners are loopback-only. Project paths must be existing canonical
directories in this allow-list; Telegram never accepts arbitrary paths.

The SQLite state database is `%LOCALAPPDATA%\codex-tg\state.db`. Back it up
only while the bridge is stopped. On restart, active turns become `faulted`;
queued prompts remain paused and are never replayed automatically. Use
`/resume` after reviewing status.

`/lock` blocks prompts until its two-minute one-time `/unlock` nonce is used.
Logs must never contain bot or App Server tokens. If Codex compatibility probe
fails, update Codex and restart; the bridge fails closed.

Troubleshooting: confirm `codex --version`, use `codex-tg status` with the
local IPC environment variables, verify the configured user/chat IDs, and
ensure no webhook remains on the bot. `autostart status` checks the per-user
`CodexTgBridge` scheduled task.

## Safe manual acceptance test

Use only a disposable repository:

```powershell
$testRoot = Join-Path $env:TEMP 'codex-tg-e2e'
New-Item -ItemType Directory -Force -Path $testRoot
git -C $testRoot init
codex-tg project add test $testRoot
codex-tg open --new $testRoot
```

Send `Create README.md containing only "bridge test".` Verify `/status`,
`/diff`, `/cancel`, approval controls, and recovery after restart. Before
deleting the directory, resolve its absolute path and verify it remains under
`$env:TEMP`.
