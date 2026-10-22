# DSH Telegram bridge

English | [Русский](README.ru.md)

A [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (DSH)
plugin that gives every DSH session its own topic in a Telegram forum group.
You can follow and drive a session from DSH or from Telegram; both sides see
the same conversation.

- **One session, one topic.** Each root session gets a topic named
  `workspace · session title`. It is renamed when DSH titles the session,
  closed when you archive it, and reopened when you restore it.
- **Both directions.** Messages you type in DSH appear in the topic. Text,
  photos, files and voice messages you send in the topic go into the session
  exactly like a message typed in the DSH composer.
- **Agent output.** Answers stream live into one message that is edited as the
  model writes. Tool calls are collapsed into one expandable block per run of
  tools. While the agent works the topic shows "typing…"; when a turn ends, a
  notifying `✅ Done` line arrives.
- **Subagents.** Subagents never create topics. Each one shows as a single
  line in its parent's topic: "working, waiting for result ⏳", then
  "done ✅" (or failed ❌ / cancelled ⏹).
- **Approvals and questions.** Approval requests and `ask_user_question`
  prompts arrive with inline buttons. Telegram and the DSH GUI race: whichever
  answers first wins, and the prompt on the other side is withdrawn.
- **Commands.** `/stop`, `/status`, `/diff`, `/compact`, `/model` and more in
  a session topic; `/sessions`, `/workspaces` and `/new` in General. Other DSH
  slash commands work too.
- **Pinned status card.** The first message of each topic (workspace, path,
  session id, model) is pinned.
- **Settings page and composer link.** Everything is configured on the
  plugin's page in DSH. The DSH composer shows a **Telegram** link that opens
  the current session's topic.

## Requirements

- DeepSeek Harness with the plugin manager (desktop app or `dsh web`).
- Node.js 22 or newer (the one DSH runs on).
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
   within 10 minutes. The group is saved, and you are added to the allowed
   users. Add more Telegram user ids on the settings page if others should be
   able to write to sessions. Messages from anyone else, from bots and from
   anonymous admins are ignored.
4. Press **Test connection**. It checks the token, the group, Topics, and the
   bot's rights.
5. Write something in any DSH session. Its topic appears in the group.

Only one process may poll a bot. If DSH desktop and `dsh web` run at the same
time with this plugin, Telegram answers `409 Conflict`; use one of them, or a
second bot.

## Using it

### Which sessions get a topic

- Every root session, new or old. By default the topic is created on the
  session's first message or turn after the plugin is running, so an older
  session gets its topic the next time you continue it. Set **Create topic**
  to "As soon as a session opens" to create it when the session is opened, or
  to "Never automatically" to create topics only with `/new` in General
  (existing topics keep working).
- The topic starts with the status card; earlier history is not copied.
- If a topic is deleted in Telegram, the next message creates a new one. A
  closed topic is reopened when the session needs it.
- Subagent sessions never get a topic.

### Talking to a session

Write in a session's topic to talk to that session. Messages queue behind the
running turn, as in DSH. Start a message with `!` to steer the running turn
instead. While the agent waits for an answer to a question, your next text
message answers it.

Photos are attached as images (when the model accepts images), documents as
files (up to **Max download, MB**). Voice messages are transcribed and sent as
text; the transcript is echoed in the topic. Without a transcriber the audio
file is attached.

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
| `/verbosity full\|tools\|final` | Output detail for this topic only. |
| `/mute`, `/unmute` | Pause or resume agent output in this topic. Approvals and questions still arrive. |
| `/steer <text>` | Same as `!text`. |
| `/help` | Command list. |

Other DSH slash commands, such as `/goal` or `/permission`, run through the
DSH command registry like in the composer. Unknown slash text is sent to the
session as a message.

### General topic commands

| Command | What it does |
|---|---|
| `/sessions` | Recent sessions, with links to those that have a topic. |
| `/workspaces` | Known DSH workspaces. |
| `/new` | Pick a workspace from buttons, then send the first message (text, photo, file or voice) or start without one. The session's topic opens with your message. |
| `/new <workspace> [prompt]` | Start directly in a workspace (by name or path). Without a prompt the bot asks for the first message. |
| `/cancel` | Stop a `/new` that is waiting for a choice or a message. |
| `/help` | Command list. |

## Settings

All settings live on the plugin's settings page and apply without a restart.

**Output**

| Setting | Default | Meaning |
|---|---|---|
| Output detail | tools collapsed | Everything with tools expanded, tools collapsed, or final answers only. `/verbosity` overrides it per topic. |
| Stream answers live | on | Edit one message while the model writes. Off sends the answer when it is complete. |
| Notify when a turn ends | on | `✅ Done` line with sound. Everything else is silent. |
| Show "typing…" while working | on | Telegram typing indicator during a turn. |
| Mirror my DSH messages | on | Show messages typed in DSH in the topic. |
| Pinned status card per topic | on | Pin the topic's first message. |

**Topics**

| Setting | Default | Meaning |
|---|---|---|
| Create topic | on first message | Or as soon as a session opens, or never automatically. |
| When a session is archived | close the topic | Close, delete, or keep it open. Restoring the session reopens a closed topic. |

**Interaction**

| Setting | Default | Meaning |
|---|---|---|
| Approvals with buttons | on | Off sends approval requests only to the DSH GUI. |
| Agent questions with buttons | on | Off sends agent questions only to the DSH GUI. |
| Send files the agent presents | on | Forward `present` files as documents. |
| Steer prefix | `!` | Empty disables the prefix. |
| Transcribe voice messages | on | Off attaches voice messages as audio files. |
| Transcription command | empty | Shell command (POSIX `sh`); `{file}` is a 16 kHz mono WAV. Example: `whisper-cli -l ru -nt -f {file}`. Without it DSH speech-to-text is used when installed. |
| Speech language (DSH STT) | empty | Language hint for DSH speech-to-text. |

**Advanced**

| Setting | Default | Meaning |
|---|---|---|
| Bridge enabled | on | Off stops polling and mirroring. |
| Stream edit interval, ms | 2500 | Minimum time between edits of a streaming message. |
| Messages per minute | 20 | Telegram's limit for one group; all topics share it. |
| Max download, MB | 20 | Largest file taken from Telegram (the Bot API limit is 20). |
| ffmpeg path | `ffmpeg` | Used to decode voice messages. |
| Bot API URL | `https://api.telegram.org` | For a self-hosted Bot API server. Must be `https`, or `http` on loopback. |

DSH speech-to-text (`@deepseek-ai/dsh-experimental-voice-input-bundle`) is
optional and does not recognize Russian; for Russian voice messages set a
transcription command such as whisper.cpp.

## Limitations

- Topics are not backfilled: a topic shows the session from the moment it was
  created.
- A topic cannot be created for an old session from Telegram; continue the
  session in DSH once, or set **Create topic** to "As soon as a session opens".
- `/model` uses DSH's model switch, which also changes the default model for
  new sessions.
- DSH has no session-deleted event, so deleting a session leaves its topic;
  archiving closes it.
- A question that timed out in Telegram can only be answered in DSH.
- One bot per running DSH process (see `409 Conflict` above).

## How it works

The plugin runs inside the DSH host process.

- It listens to DSH session events (`session/event`, `agent/assistant-stream`,
  `subagent/start`, `subagent/end`) and sends Telegram messages through one
  rate-limited queue per group. Streaming edits are coalesced so a busy group
  gets fewer, larger edits instead of a backlog. Messages that are not safe to
  repeat are retried only when Telegram certainly did not receive them.
- Input from Telegram goes through `sessionController.prompt`, the method the
  DSH composer uses. A cold session is resumed with its preset and model.
- Approvals and questions hook the `approval/request` and
  `user-questions/request` waterfalls before the GUI and race both answers.
  The DSH GUI shows the prompt at once; Telegram never delays it.
- Root sessions are told apart from subagents by the session header
  (`origin: 'subagent'`). Subagent approvals are shown in the root topic.
- The session-to-topic map and the polling offset are stored in a DSH storage
  domain (`~/.dsh/storages/telegram_bridge.json`).

## Security

- Only the bound group and the allowed user ids are served. Everything else is
  ignored, including button presses, bots and anonymous admins.
- The bot token is kept in DSH credentials and never written to logs, errors,
  configuration, or the storage domain. The settings routes cannot change the
  credential name or the Bot API URL.
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
never contact Telegram or a real DSH installation. CI runs the check on Linux
(Node 22 and 24) and Windows, plus `npm audit`.

The previous Go bridge for the Codex App Server is preserved at the `v0.1.5`
tag.

## License

[MIT](LICENSE)
