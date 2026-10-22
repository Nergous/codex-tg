# DSH Telegram bridge

English | [Русский](README.ru.md)

A [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (DSH)
plugin that gives every DSH session its own topic in a Telegram forum group.
You can follow and drive a session from DSH or from Telegram; both sides see
the same conversation.

- **One session, one topic.** A topic named `workspace · session title` is
  created when a session starts working. It is renamed when DSH titles the
  session, closed when you archive it, and reopened when you restore it.
- **Both directions.** Messages you type in DSH appear in the topic. Text you
  write in the topic goes into the session exactly like a message typed in the
  DSH composer.
- **Agent output.** Answers stream live into one message that is edited as the
  model writes. Tool calls are collapsed into one expandable block per run of
  tools. When a turn ends, a notifying `✅ Done` line arrives.
- **Subagents.** Subagents never create topics. Each one shows as a single
  line in its parent's topic: "working, waiting for result ⏳", then
  "done ✅" (or failed / cancelled).
- **Approvals and questions.** Approval requests and `ask_user_question`
  prompts arrive with inline buttons. Telegram and the DSH GUI race: whichever
  answers first wins, and the prompt on the other side is withdrawn.
- **Commands, files, voice.** See [Using it](#using-it).

## Requirements

- DeepSeek Harness with the plugin manager (desktop app or `dsh web`).
- A Telegram bot from [@BotFather](https://t.me/BotFather).
- A Telegram supergroup with **Topics** enabled.
- Optional: `ffmpeg` for voice messages.

## Install

1. In DSH open **Plugins → Add plugin** and enter one of:
   - `github:Nergous/codex-tg` (latest from GitHub), or
   - an absolute path to a local clone, for example `/home/me/projects/codex-tg`.
2. Choose **Enable now**, then open **Plugins → Telegram bridge**.

The plugin has no build step and no dependencies. After changing host code
(`index.js`, `lib/`) restart DSH; `client.js` reloads in the open page.

## Set up

1. **Bot token.** Create a bot with @BotFather (`/newbot`), paste the token on
   the settings page and press **Save token**. The token is stored in DSH
   credentials (`DSH_TELEGRAM_BOT_TOKEN`), never in configuration files. An
   environment variable of the same name also works.
2. **Group.** Create a group, open its settings and enable **Topics**. Add
   the bot and make it an administrator with **Manage topics** and
   **Pin messages**.
3. **Bind.** Press **Get bind code** and send `/bind <code>` in the group
   within 10 minutes. The group is saved, and you become the allowed user.
   Add more Telegram user ids on the settings page if others should be able to
   write to sessions. Messages from anyone else are ignored.
4. Press **Test connection**. It checks the token, the group, Topics, and the
   bot's rights.

Only one process may poll a bot. If DSH desktop and `dsh web` run at the same
time with this plugin, Telegram answers `409 Conflict`; use one of them, or a
second bot.

## Using it

Write in a session's topic to talk to that session. Messages queue behind the
running turn, as in DSH. Start a message with `!` to steer the running turn
instead. While the agent waits for an answer to a question, your next text
message answers it.

Photos are attached as images (when the model accepts images), documents as
files. Voice messages are transcribed and sent as text; the transcript is
echoed in the topic. Without a transcriber the audio file is attached.

Files the agent presents with the `present` tool are sent to the topic as
documents.

### Topic commands

| Command | What it does |
|---|---|
| `/stop` | Stop the running turn (queued messages stay queued). |
| `/status` | Workspace, state, model, context use, output mode. |
| `/diff` | `git diff --stat` of the session's directory, full patch as a file. |
| `/compact` | Compact the conversation (when the session is idle). |
| `/model [name]` | Show models as buttons, or switch to one by name. |
| `/verbosity full\|tools\|final` | Output detail for this topic. |
| `/mute`, `/unmute` | Pause or resume agent output in this topic. Approvals and questions still arrive. |
| `/steer <text>` | Same as `!text`. |
| `/help` | Command list. |

Other DSH slash commands, such as `/goal` or `/permission`, run through the
DSH command registry like in the composer. Unknown slash text is sent to the
session as a message.

### General topic commands

| Command | What it does |
|---|---|
| `/sessions` | Recent sessions with links to their topics. |
| `/workspaces` | Known DSH workspaces. |
| `/new <workspace> [prompt]` | Start a new session in a workspace (by name or path), optionally with a first message. |

## Settings

All settings live on the plugin's settings page and apply without a restart.

| Setting | Default | Meaning |
|---|---|---|
| Output detail | `tools` | `full`: tool blocks expanded. `tools`: tool blocks collapsed. `final`: only the last answer of each turn. |
| Stream answers live | on | Edit one message while the model writes. |
| Notify when a turn ends | on | `✅ Done` line with sound. Everything else is silent. |
| Mirror my DSH messages | on | Show messages typed in DSH in the topic. |
| Pinned status card | on | The topic's first message is pinned and kept up to date. |
| Create topic | on first message | Or as soon as a session opens, or never automatically. |
| When a session is archived | close | Close, delete, or keep the topic. |
| Approvals / questions with buttons | on | Off sends them only to the DSH GUI. |
| Transcribe voice messages | on | Uses the transcription command when set, otherwise DSH speech-to-text when installed. |
| Transcription command | empty | Shell command; `{file}` is a 16 kHz mono WAV. Example: `whisper-cli -l ru -nt -f {file}`. |
| Steer prefix | `!` | Empty disables the prefix. |
| Messages per minute | 20 | Telegram's limit for one group; all topics share it. |

DSH speech-to-text (`@deepseek-ai/dsh-experimental-voice-input-bundle`) is
optional and does not recognize Russian; for Russian voice messages set a
transcription command such as whisper.cpp.

## How it works

The plugin runs inside the DSH host process.

- It listens to DSH session events (`session/event`, `agent/assistant-stream`,
  `subagent/start`, `subagent/end`) and sends Telegram messages through one
  rate-limited queue per group. Streaming edits are coalesced so a busy group
  gets fewer, larger edits instead of a backlog.
- Input from Telegram goes through `sessionController.prompt`, the method the
  DSH composer uses. A cold session is resumed with its preset and model.
- Approvals and questions hook the `approval/request` and
  `user-questions/request` waterfalls before the GUI and race both answers.
- Root sessions are told apart from subagents by the session header
  (`origin: 'subagent'`). Subagent approvals are shown in the root topic.
- The session-to-topic map and the polling offset are stored in a DSH storage
  domain (`~/.dsh/storages/telegram_bridge.json`).

## Security

- Only the bound group and the allowed user ids are served. Everything else is
  ignored, including button presses.
- The bot token is kept in DSH credentials and never written to logs, errors,
  configuration, or the storage domain.
- Settings routes use DSH's own authentication.
- Anyone who can write in the group as an allowed user can run commands in
  your sessions with the sessions' permissions. Keep the group private.

See [SECURITY.md](SECURITY.md) to report a vulnerability.

## Development

```sh
npm test                      # node --test, no dependencies
node scripts/check-repo.mjs   # syntax, manifest consistency, full test suite
```

Tests use an in-process fake Telegram Bot API and fake DSH services; they
never contact Telegram or a real DSH installation.

The previous Go bridge for the Codex App Server is preserved on the
`archive/go-bridge` branch and the `v0.1.5` tag.

## License

[MIT](LICENSE)
