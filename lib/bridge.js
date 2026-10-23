/**
 * Telegram side of the bridge: one forum topic per root DSH session.
 *
 * The bridge never touches DSH directly. `index.js` translates cordis events
 * into the `on*` methods below and implements the `host` adapter that the
 * bridge calls for anything that acts on a session (send input, cancel,
 * compact, diff, model switch, new session...). That keeps all Telegram
 * behaviour testable with a fake host and a fake Bot API.
 */
import { randomBytes } from 'node:crypto'
import {
  escapeHtml,
  markdownToPlain,
  oneLine,
  renderMarkdownMessages,
  splitMarkdown,
  topicName,
  truncate,
} from './format.js'
import {
  approvalOutcomeText,
  approvalText,
  formatDuration,
  formatTokens,
  questionText,
  subagentLine,
  toolBlock,
  toolPreview,
} from './render.js'

/** Tool input reduced to its one-line preview (all the block ever shows). */
const compactInput = (input) => toolPreview(input, 200)
import { PRIORITY } from './telegram/queue.js'

export const VERBOSITY = ['full', 'tools', 'final']

/** Button labels of the DSH permission presets; unknown presets show their own name. */
const PERMISSION_LABELS = {
  'read-only': '👁 Read Only',
  'workspace-write': '✏️ Workspace Write',
  'danger-full-access': '⚠️ Full Access',
  auto: '🤖 Auto review',
}
const PERMISSION_ALIASES = {
  ro: 'read-only', read: 'read-only', readonly: 'read-only',
  ww: 'workspace-write', write: 'workspace-write', workspace: 'workspace-write',
  full: 'danger-full-access', fullaccess: 'danger-full-access', danger: 'danger-full-access',
}
export const permissionLabel = (option) => PERMISSION_LABELS[option?.value ?? option] ?? option?.name ?? String(option)

/** `ro`, `Read Only`, `full`... to a preset value from `options`, or undefined. */
export function resolvePermission(options, input) {
  const key = String(input ?? '').trim().toLowerCase().replace(/[\s_]+/g, '-')
  const compact = key.replace(/-/g, '')
  const wanted = PERMISSION_ALIASES[compact] ?? key
  return options.find((option) => option.value === wanted || String(option.name ?? '').toLowerCase().replace(/\s+/g, '-') === key)?.value
}

/** Answers longer than one message are sent as a Markdown file with a preview. */
const LONG_TEXT_FILE = 'answer.md'
const PREVIEW_CHARS = 800
const previewHtml = (markdown) => {
  const head = splitMarkdown(markdown, PREVIEW_CHARS)[0] ?? ''
  return `${renderMarkdownMessages(head)[0] ?? escapeHtml(head)}\n…\n\n📄 <i>Full answer in ${LONG_TEXT_FILE}</i>`
}

/** How long a /new picker waits for a choice and the first message. */
const NEW_FLOW_TTL_MS = 10 * 60 * 1000
/** How long /sessions buttons that create a topic stay live. */
const SESSIONS_PICKER_TTL_MS = 60 * 60 * 1000

/** Forum topic icon colours accepted by createForumTopic. */
export const TOPIC_COLORS = [7322096, 16766590, 13338331, 9367192, 16749490, 16478047]

const hash = (text) => {
  let h = 0
  for (const ch of String(text)) h = (h * 31 + ch.codePointAt(0)) >>> 0
  return h
}

export const DEFAULT_SETTINGS = Object.freeze({
  chatId: 0,
  allowedUserIds: [],
  verbosity: 'tools',
  streaming: true,
  streamIntervalMs: 2500,
  notifyOnTurnEnd: true,
  typingIndicator: true,
  mirrorGuiMessages: true,
  createTopicOn: 'first-message',
  onSessionArchived: 'close',
  onSessionDeleted: 'close',
  approvals: true,
  questions: true,
  voice: true,
  sendPresentedFiles: true,
  maxDownloadMb: 20,
  steerPrefix: '!',
  pinStatus: true,
})

const TOPIC_HELP = [
  '<b>Commands in a session topic</b>',
  '/stop — stop the running turn',
  '/status — model, context and state',
  '/diff — changed files (full diff as a file)',
  '/compact — compact the conversation',
  '/model [name] — show or switch the model',
  '/verbosity full|tools|final — output detail',
  '/permission [ro|write|full] — show or switch access (Read Only, Workspace Write, Full Access)',
  '/mute, /unmute — pause mirroring of agent output',
  '/steer &lt;text&gt; or <code>!text</code> — steer the running turn',
  '',
  'Plain text is sent to the session as your message. Photos, files and voice messages are attached.',
].join('\n')

const GENERAL_HELP = [
  '<b>DSH bridge</b>',
  '/sessions — recent sessions; tap one to open or create its topic',
  '/new — pick a workspace, then send the first message',
  '/new &lt;workspace&gt; [prompt] — start a session directly; add --ro, --write or --full to pick its access',
  '/workspaces — known workspaces',
  '/cancel — stop starting a new session',
  '/help — this help',
  '',
  'Every DSH session gets its own topic. Write in a topic to talk to that session.',
].join('\n')

export class Bridge {
  /**
   * @param {object} deps
   * @param {{ call: Function, download: Function }} deps.api - Bot API client.
   * @param {import('./telegram/queue.js').SendQueue} deps.queue - chat send queue.
   * @param {import('./store.js').BridgeStore} deps.store
   * @param {object} deps.host - DSH adapter (see index.js `createHost`).
   * @param {() => typeof DEFAULT_SETTINGS} deps.settings - live settings.
   * @param {{ info: Function, warn: Function, error: Function, debug?: Function }} [deps.logger]
   * @param {(settings: object) => Promise<void>} [deps.saveBinding] - persist chat/user binding from /bind.
   */
  constructor({ api, queue, store, host, settings, logger = console, saveBinding, ready = Promise.resolve() }) {
    /** Resolves once persistent storage is open; topics are not created before. */
    this.ready = ready
    this.api = api
    this.queue = queue
    this.store = store
    this.host = host
    this.settings = settings
    this.logger = logger
    this.saveBinding = saveBinding
    /** @type {Map<string, SessionMirror>} */
    this.mirrors = new Map()
    /** in-flight topic creation per session */
    this.creating = new Map()
    this.callbacks = new Map()
    this.callbackSeq = 0
    this.bindCode = undefined
    this.botUser = undefined
    this.timers = new Set()
    /** settle functions of approval/question prompts still waiting */
    this.openPrompts = new Set()
    /** in-flight lost-topic recovery per thread */
    this.recovering = new Map()
    /** /new flows waiting for a first message, by Telegram user id */
    this.newFlows = new Map()
    this.closed = false
  }

  // ── plumbing ─────────────────────────────────────────────────────────

  get chatId() {
    return Number(this.settings().chatId) || 0
  }

  isAllowedUser(userId) {
    const allowed = this.settings().allowedUserIds ?? []
    return allowed.map(Number).includes(Number(userId))
  }

  /**
   * A real person wrote it: not a bot, not an anonymous admin, not posted on
   * behalf of a channel or chat (those carry a shared placeholder `from`).
   */
  static isPersonalSender(message) {
    return Boolean(message?.from) && message.from.is_bot !== true && !message.sender_chat
  }

  setTimer(fn, ms) {
    const timer = setTimeout(() => {
      this.timers.delete(timer)
      if (!this.closed) fn()
    }, ms)
    this.timers.add(timer)
    return timer
  }

  clearTimer(timer) {
    if (timer === undefined) return
    clearTimeout(timer)
    this.timers.delete(timer)
  }

  close() {
    this.closed = true
    for (const timer of this.timers) clearTimeout(timer)
    this.timers.clear()
    for (const entry of this.callbacks.values()) entry.dispose?.()
    this.callbacks.clear()
    this.newFlows.clear()
    // Pending approvals/questions fall back to the DSH GUI answer.
    for (const settle of [...this.openPrompts]) settle()
    this.openPrompts.clear()
  }

  /** Bot API methods that must not be retried when the request may have landed. */
  static NON_IDEMPOTENT = new Set(['sendMessage', 'sendDocument', 'sendPhoto', 'createForumTopic'])

  /** Queue one chat-bound Bot API call. */
  send(method, params, { priority = PRIORITY.NORMAL, key } = {}) {
    return this.queue.push(() => this.api.call(method, params), { priority, key, idempotent: !Bridge.NON_IDEMPOTENT.has(method) })
  }

  /**
   * Send HTML, falling back to escaped plain text when Telegram cannot parse
   * the markup.
   */
  async sendHtml(threadId, html, options = {}) {
    try {
      return await this.sendHtmlOnce(threadId, html, options)
    } catch (error) {
      const recovered = await this.recoverThread(threadId, error).catch(() => undefined)
      if (recovered === undefined) throw error
      return this.sendHtmlOnce(recovered, html, options)
    }
  }

  /**
   * A send failed because the topic was closed or deleted in Telegram:
   * reopen it, or forget it and create a new one. Returns the thread to
   * retry in, or undefined when the error is something else.
   */
  recoverThread(threadId, error) {
    if (threadId === undefined || !this.chatId) return Promise.resolve(undefined)
    const closed = error?.isTopicClosed
    const gone = error?.isGone && /thread|topic/i.test(error.description ?? '')
    if (!closed && !gone) return Promise.resolve(undefined)
    if (this.recovering.has(threadId)) return this.recovering.get(threadId)
    const run = (async () => {
      const sessionId = this.store.sessionForThread(this.chatId, threadId)
      if (!sessionId) return undefined
      if (closed) {
        await this.store.patchTopic(sessionId, { closed: false })
        await this.send('reopenForumTopic', { chat_id: this.chatId, message_thread_id: threadId }, { priority: PRIORITY.HIGH }).catch(() => undefined)
        return threadId
      }
      this.logger.info?.(`telegram bridge: topic of ${sessionId} was deleted in Telegram; creating a new one`)
      await this.store.deleteTopic(sessionId)
      this.mirrors.get(sessionId)?.forgetMessages()
      return this.ensureTopic(sessionId)
    })().finally(() => this.recovering.delete(threadId))
    this.recovering.set(threadId, run)
    return run
  }

  async sendHtmlOnce(threadId, html, { priority = PRIORITY.NORMAL, silent = true, replyMarkup, plain, replyTo } = {}) {
    const params = {
      chat_id: this.chatId,
      message_thread_id: threadId,
      text: html,
      parse_mode: 'HTML',
      disable_notification: silent,
      link_preview_options: { is_disabled: true },
      reply_markup: replyMarkup,
      reply_parameters: replyTo ? { message_id: replyTo, allow_sending_without_reply: true } : undefined,
    }
    try {
      return await this.send('sendMessage', params, { priority })
    } catch (error) {
      if (!error?.isParseError) throw error
      return this.send('sendMessage', { ...params, parse_mode: undefined, text: plain ?? html.replace(/<[^>]+>/g, '') }, { priority })
    }
  }

  async editHtml(messageId, html, { priority = PRIORITY.LOW, key, replyMarkup, plain } = {}) {
    const params = {
      chat_id: this.chatId,
      message_id: messageId,
      text: html,
      parse_mode: 'HTML',
      link_preview_options: { is_disabled: true },
      reply_markup: replyMarkup,
    }
    try {
      return await this.send('editMessageText', params, { priority, key })
    } catch (error) {
      if (error?.isNotModified) return undefined
      if (error?.isParseError) {
        try {
          return await this.send('editMessageText', { ...params, parse_mode: undefined, text: plain ?? html.replace(/<[^>]+>/g, '') }, { priority, key })
        } catch (inner) {
          if (inner?.isNotModified) return undefined
          throw inner
        }
      }
      throw error
    }
  }

  report(error, what) {
    if (this.closed) return
    this.logger.warn?.(`telegram bridge: ${what}: ${error?.message ?? error}`)
  }

  mirror(sessionId) {
    let mirror = this.mirrors.get(sessionId)
    if (!mirror) {
      mirror = new SessionMirror(this, sessionId)
      this.mirrors.set(sessionId, mirror)
    }
    return mirror
  }

  // ── topics ───────────────────────────────────────────────────────────

  /**
   * Thread id of the session's topic, creating the topic when needed.
   * Returns undefined when the bridge is not bound to a chat yet.
   */
  async ensureTopic(sessionId) {
    await this.ready
    if (!this.chatId || this.closed) return undefined
    const existing = this.store.topic(sessionId)
    if (existing && existing.chatId === this.chatId) {
      if (existing.closed) await this.reopenTopic(sessionId)
      return existing.threadId
    }
    if (this.creating.has(sessionId)) return this.creating.get(sessionId)
    const run = this.createTopic(sessionId).finally(() => this.creating.delete(sessionId))
    this.creating.set(sessionId, run)
    return run
  }

  async createTopic(sessionId) {
    const info = (await this.host.sessionInfo(sessionId)) ?? {}
    const name = topicName(info.workspaceTitle, info.title || 'New session')
    const topic = await this.send('createForumTopic', {
      chat_id: this.chatId,
      name,
      icon_color: TOPIC_COLORS[hash(info.cwd ?? info.workspaceTitle ?? '') % TOPIC_COLORS.length],
    }, { priority: PRIORITY.HIGH })
    const threadId = topic.message_thread_id
    await this.store.putTopic(sessionId, {
      chatId: this.chatId,
      threadId,
      name,
      cwd: info.cwd,
      closed: false,
      createdAt: Date.now(),
    })
    const lines = [`🧵 <b>${escapeHtml(info.workspaceTitle ?? 'workspace')}</b>`]
    if (info.cwd) lines.push(`<code>${escapeHtml(info.cwd)}</code>`)
    lines.push(`Session <code>${escapeHtml(sessionId)}</code>`)
    if (info.model) lines.push(`Model: ${escapeHtml(info.model)}`)
    lines.push('Write here to talk to this session. /help for commands.')
    this.sendHtml(threadId, lines.join('\n'), { priority: PRIORITY.HIGH })
      .then((message) => (this.settings().pinStatus ? this.pinStatus(sessionId, message?.message_id) : undefined))
      .catch((error) => this.report(error, 'topic header'))
    return threadId
  }

  async pinStatus(sessionId, messageId) {
    if (!messageId) return
    await this.store.patchTopic(sessionId, { statusMessageId: messageId })
    await this.send('pinChatMessage', { chat_id: this.chatId, message_id: messageId, disable_notification: true }, { priority: PRIORITY.LOW }).catch(() => undefined)
  }

  async renameTopic(sessionId) {
    const record = this.store.topic(sessionId)
    if (!record || record.chatId !== this.chatId) return
    const info = (await this.host.sessionInfo(sessionId)) ?? {}
    if (!info.title) return
    const name = topicName(info.workspaceTitle, info.title)
    if (name === record.name) return
    await this.store.patchTopic(sessionId, { name })
    await this.send('editForumTopic', { chat_id: this.chatId, message_thread_id: record.threadId, name }, { priority: PRIORITY.LOW, key: `topic-name:${sessionId}` })
  }

  async closeTopic(sessionId, mode = 'close') {
    const record = this.store.topic(sessionId)
    if (!record || record.chatId !== this.chatId) return
    if (mode === 'delete') {
      await this.send('deleteForumTopic', { chat_id: this.chatId, message_thread_id: record.threadId }, { priority: PRIORITY.LOW }).catch((error) => this.report(error, 'delete topic'))
      await this.store.deleteTopic(sessionId)
      this.mirrors.delete(sessionId)
      return
    }
    if (record.closed) return
    await this.store.patchTopic(sessionId, { closed: true })
    await this.send('closeForumTopic', { chat_id: this.chatId, message_thread_id: record.threadId }, { priority: PRIORITY.LOW }).catch((error) => this.report(error, 'close topic'))
  }

  async reopenTopic(sessionId) {
    const record = this.store.topic(sessionId)
    if (!record || !record.closed) return
    await this.store.patchTopic(sessionId, { closed: false })
    await this.send('reopenForumTopic', { chat_id: this.chatId, message_thread_id: record.threadId }, { priority: PRIORITY.HIGH }).catch((error) => this.report(error, 'reopen topic'))
  }

  topicLink(threadId) {
    const id = String(this.chatId).replace(/^-100/, '')
    return `https://t.me/c/${id}/${threadId}`
  }

  // ── DSH events (called by index.js) ───────────────────────────────────

  /** A root session became active (first message or turn). */
  onSessionActive(sessionId) {
    if (!this.chatId) return
    this.ensureTopic(sessionId).catch((error) => this.report(error, 'create topic'))
  }

  /** A root agent was created (session started or resumed). */
  onSessionStarted(sessionId) {
    if (this.settings().createTopicOn === 'agent-created') this.onSessionActive(sessionId)
  }

  onTitleChanged(sessionId) {
    this.renameTopic(sessionId).catch((error) => this.report(error, 'rename topic'))
  }

  onSessionArchived(sessionId) {
    const mode = this.settings().onSessionArchived
    if (mode === 'keep') return
    this.closeTopic(sessionId, mode).catch((error) => this.report(error, 'archive topic'))
  }

  onSessionRestored(sessionId) {
    this.reopenTopic(sessionId).catch((error) => this.report(error, 'restore topic'))
  }

  onSessionDeleted(sessionId) {
    const mode = this.settings().onSessionDeleted
    if (mode === 'keep') return
    this.closeTopic(sessionId, mode).catch((error) => this.report(error, 'delete topic'))
  }

  /** A user message entered the session log. `source.kind` tells where it came from. */
  onUserMessage(sessionId, { text, source, images = 0 }) {
    if (source?.kind === 'telegram') return this.mirror(sessionId).closeSegments()
    if (source?.kind !== 'user') return undefined
    if (this.settings().createTopicOn !== 'never') this.onSessionActive(sessionId)
    if (!this.settings().mirrorGuiMessages) return this.mirror(sessionId).closeSegments()
    return this.mirror(sessionId).userMessage(text, images)
  }

  onTurnStarted(sessionId) {
    if (this.settings().createTopicOn !== 'never') this.onSessionActive(sessionId)
    this.mirror(sessionId).turnStarted()
  }

  onTurnEnded(sessionId, details = {}) {
    this.mirror(sessionId).turnEnded(details)
  }

  /** Live assistant text so far for one model attempt. */
  onAssistantDelta(sessionId, attemptKey, text) {
    this.mirror(sessionId).assistantDelta(attemptKey, text)
  }

  /** Committed assistant message: final text plus the tool calls it made. */
  onAssistantMessage(sessionId, { attemptKey, text, toolCalls = [] }) {
    this.mirror(sessionId).assistantMessage(attemptKey, text, toolCalls)
  }

  /** A streamed attempt was retried or failed: drop its draft messages. */
  onAssistantAbandoned(sessionId, attemptKey) {
    this.mirrors.get(sessionId)?.abandonText(attemptKey)
  }

  onSubagentLabel(rootSessionId, childId, label) {
    this.mirrors.get(rootSessionId)?.subagentLabel(childId, label)
  }

  onToolResult(sessionId, { callId, isError, text }) {
    this.mirror(sessionId).toolResult(callId, isError, text)
  }

  onSubagentStarted(rootSessionId, childId, info) {
    this.mirror(rootSessionId).subagentStarted(childId, info)
  }

  onSubagentEnded(rootSessionId, childId, info) {
    this.mirror(rootSessionId).subagentEnded(childId, info)
  }

  onFilesPresented(sessionId, files) {
    if (!this.settings().sendPresentedFiles) return
    this.mirror(sessionId).sendFiles(files)
  }

  // ── approvals ────────────────────────────────────────────────────────

  /** Topic for an interactive prompt; with createTopicOn 'never' only an existing one. */
  async promptThread(sessionId) {
    if (this.settings().createTopicOn !== 'never') return this.ensureTopic(sessionId)
    const record = this.store.topic(sessionId)
    if (!record || record.chatId !== this.chatId) return undefined
    if (record.closed) await this.reopenTopic(sessionId)
    return record.threadId
  }

  registerCallback(entry) {
    this.callbackSeq = (this.callbackSeq + 1) % 1e9
    const id = this.callbackSeq.toString(36)
    this.callbacks.set(id, entry)
    return id
  }

  /**
   * Post an approval prompt. Resolves once the prompt is delivered (or
   * undefined when it could not be). `answer` resolves with `'allowed-once'`
   * or `'rejected'` when pressed in Telegram, or `undefined` when the bridge
   * shuts down. `settle()` records the final outcome (from either side).
   * @returns {Promise<{ answer: Promise<string | undefined>, settle: (outcome: string, via?: string) => void } | undefined>}
   */
  async requestApproval(sessionId, req) {
    if (!this.settings().approvals || !this.chatId || this.closed) return undefined
    const threadId = await this.promptThread(sessionId)
    if (threadId === undefined) return undefined
    let resolveAnswer
    const answer = new Promise((resolve) => { resolveAnswer = resolve })
    let messageId
    let settled = false
    let pressed
    const id = this.registerCallback({
      kind: 'approval',
      sessionId,
      onPress: (action) => {
        if (settled || pressed) return 'Already answered'
        pressed = action === 'a' ? 'allowed-once' : 'rejected'
        resolveAnswer(pressed)
        return pressed === 'allowed-once' ? 'Allowed' : 'Rejected'
      },
    })
    const settle = (outcome, via) => {
      if (settled) return
      settled = true
      this.callbacks.delete(id)
      this.openPrompts.delete(onClose)
      resolveAnswer(undefined)
      if (messageId && outcome) this.editHtml(messageId, approvalOutcomeText(req, outcome, via), { priority: PRIORITY.NORMAL }).catch((error) => this.report(error, 'approval outcome'))
    }
    const onClose = () => settle(undefined)
    this.openPrompts.add(onClose)
    const keyboard = { inline_keyboard: [[{ text: '✅ Allow', callback_data: `ap:${id}:a` }, { text: '🚫 Deny', callback_data: `ap:${id}:r` }]] }
    try {
      const message = await this.sendHtml(threadId, approvalText(req), { priority: PRIORITY.HIGH, silent: false, replyMarkup: keyboard })
      messageId = message?.message_id
    } catch (error) {
      this.report(error, 'approval prompt')
      settle(undefined)
      return undefined
    }
    return { answer, settle }
  }

  // ── questions ────────────────────────────────────────────────────────

  /**
   * Post an ask_user_question batch; resolves once the prompts are delivered
   * (undefined when they could not be). `answer` resolves with
   * `[{ id, selected: string[], custom?: string }]` once every question is
   * answered in Telegram, or undefined when the bridge shuts down.
   * `cancel(via)` withdraws the prompts.
   */
  async requestQuestions(sessionId, questions) {
    if (!this.settings().questions || !this.chatId || !questions?.length || this.closed) return undefined
    const threadId = await this.promptThread(sessionId)
    if (threadId === undefined) return undefined
    const mirror = this.mirror(sessionId)
    let resolveAll
    const answer = new Promise((resolve) => { resolveAll = resolve })
    const state = questions.map((question) => ({ question, selected: new Set(), done: false, custom: undefined, messageId: undefined }))
    let finished = false
    const finish = () => {
      if (finished || !state.every((item) => item.done)) return
      finished = true
      this.callbacks.delete(id)
      this.openPrompts.delete(onClose)
      if (mirror.pendingQuestion === pending) mirror.pendingQuestion = undefined
      resolveAll(state.map((item) => ({
        id: item.question.id,
        selected: item.custom !== undefined && !(item.question.multi_select || item.question.multiSelect) ? [] : [...item.selected],
        ...item.custom !== undefined ? { custom: item.custom } : {},
      })))
    }
    const keyboardOf = (index) => {
      const item = state[index]
      const multi = item.question.multi_select || item.question.multiSelect
      const rows = (item.question.options ?? []).map((option, oi) => [{
        text: `${multi ? (item.selected.has(option.label) ? '☑ ' : '☐ ') : ''}${truncate(option.label, 60)}`,
        callback_data: `q:${id}:${index}:${oi}`,
      }])
      if (multi) rows.push([{ text: '✔️ Done', callback_data: `q:${id}:${index}:d` }])
      return { inline_keyboard: rows }
    }
    const answeredText = (index) => {
      const item = state[index]
      const picked = [...item.selected]
      if (item.custom !== undefined) picked.push(`“${item.custom}”`)
      return `${questionText(item.question, index, state.length)}\n\n<b>Answer:</b> ${escapeHtml(picked.join(', ') || '—')}`
    }
    const markDone = (index) => {
      const item = state[index]
      if (item.done) return
      item.done = true
      if (item.messageId) this.editHtml(item.messageId, answeredText(index), { priority: PRIORITY.NORMAL }).catch((error) => this.report(error, 'question answered'))
      finish()
    }
    const id = this.registerCallback({
      kind: 'question',
      sessionId,
      onPress: (action, rest) => {
        const index = Number(action)
        const item = state[index]
        if (!item || item.done || finished) return 'Already answered'
        const multi = item.question.multi_select || item.question.multiSelect
        if (rest === 'd') {
          markDone(index)
          return 'Done'
        }
        const option = item.question.options?.[Number(rest)]
        if (!option) return 'Unknown option'
        if (!multi) {
          item.selected = new Set([option.label])
          markDone(index)
          return option.label
        }
        if (item.selected.has(option.label)) item.selected.delete(option.label)
        else item.selected.add(option.label)
        if (item.messageId) this.send('editMessageReplyMarkup', { chat_id: this.chatId, message_id: item.messageId, reply_markup: keyboardOf(index) }, { priority: PRIORITY.HIGH, key: `qkb:${id}:${index}` }).catch(() => undefined)
        return option.label
      },
    })
    const pending = {
      /** Plain text in the topic answers the first open question. */
      answerText: (text) => {
        const index = state.findIndex((item) => !item.done)
        if (index < 0) return false
        state[index].custom = text
        markDone(index)
        return true
      },
    }
    const cancel = (via) => {
      if (finished) return
      finished = true
      this.callbacks.delete(id)
      this.openPrompts.delete(onClose)
      if (mirror.pendingQuestion === pending) mirror.pendingQuestion = undefined
      resolveAll(undefined)
      if (via === null) return
      for (const [index, item] of state.entries()) {
        if (item.messageId && !item.done) this.editHtml(item.messageId, `${questionText(item.question, index, state.length)}\n\n<i>${escapeHtml(via ?? 'Answered elsewhere')}</i>`, { priority: PRIORITY.NORMAL }).catch(() => undefined)
      }
    }
    const onClose = () => cancel(null)
    this.openPrompts.add(onClose)
    mirror.pendingQuestion = pending
    try {
      for (const [index, item] of state.entries()) {
        const message = await this.sendHtml(threadId, questionText(item.question, index, state.length), { priority: PRIORITY.HIGH, silent: index > 0, replyMarkup: keyboardOf(index) })
        item.messageId = message?.message_id
      }
    } catch (error) {
      this.report(error, 'question prompt')
      cancel('Could not deliver all questions; answer in DSH')
      return undefined
    }
    return { answer, cancel }
  }

  // ── inbound ──────────────────────────────────────────────────────────

  /** Handle one getUpdates update. Never throws for user input problems. */
  async handleUpdate(update) {
    if (update.callback_query) return this.handleCallback(update.callback_query)
    const message = update.message
    if (!message) return undefined
    const text = message.text ?? message.caption ?? ''
    // Pairing works before the chat is bound.
    const bind = /^\/bind(?:@\w+)?\s+(\S+)/.exec(text)
    if (bind) return this.handleBind(message, bind[1])
    if (!this.chatId || message.chat?.id !== this.chatId) return undefined
    if (!Bridge.isPersonalSender(message) || !this.isAllowedUser(message.from.id)) return undefined
    if (message.forum_topic_created || message.forum_topic_edited || message.forum_topic_closed || message.forum_topic_reopened || message.pinned_message) return undefined
    const threadId = message.is_topic_message ? message.message_thread_id : undefined
    if (threadId === undefined) return this.handleGeneral(message, text)
    const sessionId = this.store.sessionForThread(this.chatId, threadId)
    if (!sessionId) {
      if (text.startsWith('/') || this.newFlows.get(message.from.id)?.threadId === threadId) return this.handleGeneral(message, text, threadId)
      return this.sendHtml(threadId, 'This topic is not linked to a DSH session.', { replyTo: message.message_id })
    }
    return this.mirror(sessionId).inbound(message, text)
  }

  async handleBind(message, code) {
    if (!this.bindCode || code !== this.bindCode.code || Date.now() > this.bindCode.expiresAt) return undefined
    const chat = message.chat
    if (!Bridge.isPersonalSender(message)) {
      await this.api.call('sendMessage', { chat_id: chat.id, text: 'Send /bind as yourself, not anonymously or as a channel.' }).catch(() => undefined)
      return undefined
    }
    if (chat?.type !== 'supergroup') {
      await this.api.call('sendMessage', { chat_id: chat.id, text: 'Bind the bot in a supergroup with Topics enabled.' }).catch(() => undefined)
      return undefined
    }
    if (!chat.is_forum) {
      await this.api.call('sendMessage', { chat_id: chat.id, text: 'Enable Topics in the group settings, then send /bind again.' }).catch(() => undefined)
      return undefined
    }
    this.bindCode = undefined
    const allowed = new Set((this.settings().allowedUserIds ?? []).map(Number))
    allowed.add(message.from.id)
    await this.saveBinding?.({ chatId: chat.id, allowedUserIds: [...allowed] })
    await this.api.call('sendMessage', {
      chat_id: chat.id,
      text: `✅ Bound to DSH. Sessions will appear here as topics. Allowed user: ${message.from.id}.`,
      message_thread_id: message.is_topic_message ? message.message_thread_id : undefined,
    }).catch((error) => this.report(error, 'bind reply'))
    return { chatId: chat.id, userId: message.from.id }
  }

  /** One-time pairing code shown in the DSH settings page. */
  createBindCode(ttlMs = 10 * 60 * 1000) {
    const code = randomBytes(8).toString('hex')
    this.bindCode = { code, expiresAt: Date.now() + ttlMs }
    return { ...this.bindCode }
  }

  async handleCallback(query) {
    const answer = (text) => this.api.call('answerCallbackQuery', { callback_query_id: query.id, text: text ? truncate(text, 190) : undefined }).catch(() => undefined)
    if (query.from?.is_bot || !this.isAllowedUser(query.from?.id) || query.message?.chat?.id !== this.chatId) return answer('Not allowed')
    const [, id, action, rest] = /^(?:ap|q|cmd):([^:]+):([^:]*)(?::(.*))?$/.exec(query.data ?? '') ?? []
    const entry = id === undefined ? undefined : this.callbacks.get(id)
    if (!entry) {
      if (query.message?.message_id) this.send('editMessageReplyMarkup', { chat_id: this.chatId, message_id: query.message.message_id, reply_markup: { inline_keyboard: [] } }, { priority: PRIORITY.LOW }).catch(() => undefined)
      return answer('This prompt has expired')
    }
    let reply
    try {
      reply = await entry.onPress(action, rest, query)
    } catch (error) {
      reply = `Error: ${error?.message ?? error}`
    }
    return answer(reply)
  }

  async handleGeneral(message, text, threadId) {
    const reply = (html) => this.sendHtml(threadId, html, { replyTo: message.message_id, priority: PRIORITY.HIGH })
    const command = /^\/(\w+)(?:@\w+)?(?:\s+([\s\S]*))?$/.exec(text.trim())
    const isCommand = command && !message.photo && !message.document && !message.voice
    const flow = this.newFlows.get(message.from?.id)
    if (!isCommand && flow?.prompting && flow.threadId === threadId) return this.launchFromFlow(flow, message, text)
    if (!isCommand) return threadId === undefined ? reply('Write inside a session topic, or use /new and /sessions here. /help for more.') : undefined
    const [, name, argText = ''] = command
    switch (name.toLowerCase()) {
      case 'start':
      case 'help':
        return reply(GENERAL_HELP)
      case 'cancel':
        if (!flow) return reply('Nothing to cancel.')
        this.endFlow(flow, '✖ Cancelled.')
        return reply('✖ Cancelled.')
      case 'sessions': {
        const sessions = (await this.host.listSessions({ limit: 15 })) ?? []
        if (sessions.length === 0) return reply('No sessions yet.')
        return this.offerSessions(message, threadId, sessions)
      }
      case 'workspaces': {
        const workspaces = (await this.host.listWorkspaces()) ?? []
        if (workspaces.length === 0) return reply('No workspaces.')
        return reply(`<b>Workspaces</b>\n${workspaces.map((ws) => `• <b>${escapeHtml(ws.title)}</b> <code>${escapeHtml(ws.path)}</code>`).join('\n')}`)
      }
      case 'new': {
        const workspaces = (await this.host.listWorkspaces()) ?? []
        const words = argText.trim().split(/\s+/).filter(Boolean)
        // `--ro`, `--write`, `--full` (or any preset name) picks the access for the new session.
        let permission
        const flagAt = words.findIndex((word) => word.startsWith('--'))
        if (flagAt >= 0) {
          const options = (await this.host.permissions?.())?.options ?? []
          permission = resolvePermission(options, words[flagAt].slice(2))
          if (!permission) return reply(`Unknown access <code>${escapeHtml(words[flagAt])}</code>. Use --ro, --write or --full.`)
          words.splice(flagAt, 1)
        }
        const [target, ...rest] = words
        if (!target) {
          if (workspaces.length === 0) return reply('No workspaces yet. Open a folder in DSH first.')
          return this.offerWorkspaces(message, threadId, workspaces)
        }
        const lower = target.toLowerCase()
        const workspace = workspaces.find((ws) => ws.path === target) ?? workspaces.find((ws) => ws.title?.toLowerCase() === lower) ?? workspaces.find((ws) => ws.title?.toLowerCase().includes(lower))
        if (!workspace) return reply(`Unknown workspace <code>${escapeHtml(target)}</code>. /workspaces lists them, or send /new to pick one.`)
        const prompt = rest.join(' ').trim()
        if (!prompt) return this.offerWorkspaces(message, threadId, workspaces, workspace, permission)
        try {
          const { link } = await this.launchSession(workspace, { message, text: prompt }, { permission })
          return reply(`🆕 Session started in <b>${escapeHtml(workspace.title)}</b>${link ? ` — <a href="${link}">open topic</a>` : ''}`)
        } catch (error) {
          return reply(`⚠️ Cannot start a session: ${escapeHtml(error?.message ?? String(error))}`)
        }
      }
      default:
        return reply(`Unknown command /${escapeHtml(name)}. /help`)
    }
  }

  // ── /sessions: continue any DSH session from Telegram ────────────────

  /** Open (not closed) topic link of a session in the bound chat, if any. */
  openTopicLink(sessionId) {
    const record = this.store.topic(sessionId)
    return record && record.chatId === this.chatId && !record.closed ? this.topicLink(record.threadId) : undefined
  }

  /**
   * Recent sessions as buttons: a session with a topic links straight to it;
   * one without gets a button that creates (or reopens) its topic and then
   * posts the link.
   */
  async offerSessions(message, threadId, sessions) {
    let messageId
    const id = this.registerCallback({
      kind: 'sessions',
      onPress: async (action) => {
        const session = sessions[Number(action)]
        if (!session) return 'Unknown session'
        const topicThread = await this.ensureTopic(session.sessionId)
        if (topicThread === undefined) return 'Could not open a topic'
        const title = session.title || session.sessionId
        this.sendHtml(threadId, `🧵 <b>${escapeHtml(truncate(title, 80))}</b> — ${escapeHtml(session.workspaceTitle ?? '')}\n<a href="${this.topicLink(topicThread)}">Open topic</a>`, { priority: PRIORITY.HIGH }).catch((error) => this.report(error, 'session link'))
        if (messageId) this.send('editMessageReplyMarkup', { chat_id: this.chatId, message_id: messageId, reply_markup: keyboard() }, { priority: PRIORITY.LOW, key: `sessions:${id}` }).catch(() => undefined)
        return `Topic ready: ${truncate(title, 60)}`
      },
    })
    this.setTimer(() => this.callbacks.delete(id), SESSIONS_PICKER_TTL_MS)
    const keyboard = () => ({
      inline_keyboard: sessions.map((session, index) => {
        const link = this.openTopicLink(session.sessionId)
        const label = truncate(`${link ? '🧵' : '➕'} ${session.title || session.sessionId}${session.workspaceTitle ? ` · ${session.workspaceTitle}` : ''}`, 60) + (session.running ? ' ⏳' : '')
        return [link ? { text: label, url: link } : { text: label, callback_data: `cmd:${id}:${index}` }]
      }),
    })
    const sent = await this.sendHtml(threadId, '<b>Recent sessions</b>\n🧵 opens the topic · ➕ creates one to continue the session here', { replyTo: message.message_id, priority: PRIORITY.HIGH, replyMarkup: keyboard() })
    messageId = sent?.message_id
    return sent
  }

  // ── /new flow: pick a workspace, then send the first message ─────────

  /**
   * Post the workspace picker, or (with `chosen`) go straight to asking for
   * the first message. One flow per user; a new /new replaces the old one.
   */
  async offerWorkspaces(message, threadId, workspaces, chosen, permission) {
    const userId = message.from.id
    const previous = this.newFlows.get(userId)
    if (previous) this.endFlow(previous, '✖ Replaced by a newer /new.')
    const permissions = await this.host.permissions?.().catch(() => undefined)
    const flow = { userId, threadId, workspaces, workspace: undefined, prompting: false, messageId: undefined, callbackId: undefined, timer: undefined, accessOptions: permissions?.options ?? [], permission }
    flow.callbackId = this.registerCallback({
      kind: 'new',
      onPress: (action, rest, query) => this.onFlowPress(flow, action, rest, query),
    })
    flow.timer = this.setTimer(() => this.endFlow(flow, '⌛ Expired. Send /new again.'), NEW_FLOW_TTL_MS)
    this.newFlows.set(userId, flow)
    if (chosen) {
      flow.workspace = chosen
      flow.prompting = true
    }
    const view = this.flowView(flow)
    try {
      const sent = await this.sendHtml(threadId, view.html, { replyTo: message.message_id, priority: PRIORITY.HIGH, replyMarkup: view.keyboard })
      flow.messageId = sent?.message_id
    } catch (error) {
      this.endFlow(flow)
      throw error
    }
    return flow
  }

  flowView(flow) {
    const cb = (action, rest) => `cmd:${flow.callbackId}:${action}${rest === undefined ? '' : `:${rest}`}`
    if (!flow.workspace) {
      const buttons = flow.workspaces.slice(0, 40).map((ws, index) => ({ text: truncate(ws.title || ws.path, 40), callback_data: cb('w', index) }))
      const rows = []
      for (let i = 0; i < buttons.length; i += 2) rows.push(buttons.slice(i, i + 2))
      rows.push([{ text: '✖ Cancel', callback_data: cb('x') }])
      return { html: '🆕 <b>New session</b>\nPick a workspace:', keyboard: { inline_keyboard: rows } }
    }
    const ws = flow.workspace
    const chosen = flow.accessOptions.find((option) => option.value === flow.permission)
    const access = flow.accessOptions.length ? `\nAccess: <b>${escapeHtml(chosen ? permissionLabel(chosen) : 'DSH default')}</b>` : ''
    const accessRow = flow.accessOptions.map((option) => ({ text: `${permissionLabel(option)}${option.value === flow.permission ? ' ✓' : ''}`, callback_data: cb('p', option.value) }))
    const rows = []
    for (let i = 0; i < accessRow.length; i += 2) rows.push(accessRow.slice(i, i + 2))
    rows.push([{ text: '▶️ Start without a message', callback_data: cb('e') }], [{ text: '↩️ Other workspace', callback_data: cb('b') }, { text: '✖ Cancel', callback_data: cb('x') }])
    return {
      html: `🆕 <b>New session in ${escapeHtml(ws.title || ws.path)}</b>\n<code>${escapeHtml(ws.path)}</code>${access}\n\nSend the first message now: text, photo, file or voice.`,
      keyboard: { inline_keyboard: rows },
    }
  }

  async onFlowPress(flow, action, rest, query) {
    if (this.newFlows.get(flow.userId) !== flow) return 'This prompt has expired'
    if (query?.from?.id !== flow.userId) return 'Only the person who sent /new can use this'
    const redraw = () => {
      if (!flow.messageId) return
      const view = this.flowView(flow)
      this.editHtml(flow.messageId, view.html, { priority: PRIORITY.HIGH, replyMarkup: view.keyboard }).catch((error) => this.report(error, 'new session picker'))
    }
    switch (action) {
      case 'w': {
        const workspace = flow.workspaces[Number(rest)]
        if (!workspace) return 'Unknown workspace'
        flow.workspace = workspace
        flow.prompting = true
        redraw()
        return workspace.title || workspace.path
      }
      case 'p': {
        const option = flow.accessOptions.find((entry) => entry.value === rest)
        if (!option) return 'Unknown access mode'
        flow.permission = option.value
        redraw()
        return `Access: ${permissionLabel(option)}`
      }
      case 'b':
        flow.workspace = undefined
        flow.prompting = false
        redraw()
        return 'Pick a workspace'
      case 'e':
        if (!flow.workspace) return 'Pick a workspace first'
        this.launchFromFlow(flow, undefined, '').catch((error) => this.report(error, 'new session'))
        return 'Starting…'
      case 'x':
        this.endFlow(flow, '✖ Cancelled.')
        return 'Cancelled'
      default:
        return 'Unknown action'
    }
  }

  /** Finish a flow; with `text`, replace the picker message (and drop its buttons). */
  endFlow(flow, text) {
    if (this.newFlows.get(flow.userId) === flow) this.newFlows.delete(flow.userId)
    this.callbacks.delete(flow.callbackId)
    this.clearTimer(flow.timer)
    flow.prompting = false
    if (text && flow.messageId) this.editHtml(flow.messageId, text, { priority: PRIORITY.NORMAL, replyMarkup: { inline_keyboard: [] } }).catch(() => undefined)
  }

  /** The user's first message (or none) for the chosen workspace: start the session. */
  async launchFromFlow(flow, message, text) {
    const workspace = flow.workspace
    this.endFlow(flow, `⏳ Starting a session in <b>${escapeHtml(workspace.title || workspace.path)}</b>…`)
    try {
      const { link } = await this.launchSession(workspace, message ? { message, text } : undefined, { permission: flow.permission })
      const html = `🆕 Session started in <b>${escapeHtml(workspace.title || workspace.path)}</b>${link ? ` — <a href="${link}">open topic</a>` : ''}`
      if (flow.messageId) await this.editHtml(flow.messageId, html, { priority: PRIORITY.HIGH, replyMarkup: { inline_keyboard: [] } })
      else await this.sendHtml(flow.threadId, html, { priority: PRIORITY.HIGH })
    } catch (error) {
      const html = `⚠️ Cannot start a session: ${escapeHtml(error?.message ?? String(error))}`
      if (flow.messageId) await this.editHtml(flow.messageId, html, { priority: PRIORITY.HIGH, replyMarkup: { inline_keyboard: [] } }).catch(() => undefined)
      else await this.sendHtml(flow.threadId, html, { priority: PRIORITY.HIGH }).catch(() => undefined)
    }
  }

  /**
   * Create a session in `workspace`, open its topic, and deliver the first
   * Telegram message (text, photo, file or voice) like one written in the
   * topic. The message is echoed in the topic so it starts with the prompt.
   */
  async launchSession(workspace, first, { permission } = {}) {
    const sessionId = await this.host.newSession(workspace.path, undefined, permission ? { permission } : undefined)
    const threadId = await this.ensureTopic(sessionId)
    if (first?.message) {
      const mirror = this.mirror(sessionId)
      const message = first.message
      const media = message.photo ? '🖼 photo' : message.voice ? '🎙 voice message' : message.document ? `📎 ${message.document.file_name ?? 'file'}` : ''
      const body = [media, truncate(String(first.text ?? ''), 3500)].filter(Boolean).join('\n')
      if (threadId !== undefined && body) mirror.enqueue((thread) => this.sendHtml(thread, `👤 <b>You (Telegram)</b>\n${escapeHtml(body)}`, { priority: PRIORITY.HIGH }))
      await mirror.inbound(message, first.text ?? '')
    }
    return { sessionId, threadId, link: threadId === undefined ? undefined : this.topicLink(threadId) }
  }
}

/** Per-session output state: the topic's message segments for the current turn. */
class SessionMirror {
  constructor(bridge, sessionId) {
    this.bridge = bridge
    this.sessionId = sessionId
    /** Open text segment (assistant output being streamed/sent). */
    this.text = undefined
    /** Open tool block segment. */
    this.tools = undefined
    this.segmentSeq = 0
    this.toolIndex = new Map()
    this.subagents = new Map()
    this.running = false
    this.turnStartedAt = 0
    this.lastAssistantText = ''
    this.typingTimer = undefined
    this.pendingQuestion = undefined
    this.queueTail = Promise.resolve()
    /**
     * Text segments closed while their attempt may still be streaming (a
     * user message arrived mid-answer), by attempt key. The same attempt
     * keeps editing its own messages instead of sending them again.
     */
    this.sealed = new Map()
  }

  /** The topic was recreated: old message ids point into a deleted thread. */
  forgetMessages() {
    for (const segment of [this.text, ...this.sealed.values()]) if (segment) this.bridge.clearTimer(segment.timer)
    this.text = undefined
    this.tools = undefined
    this.sealed.clear()
    this.toolIndex.clear()
    for (const sub of this.subagents.values()) sub.messageId = undefined
  }

  get settings() {
    return this.bridge.settings()
  }

  get record() {
    const record = this.bridge.store.topic(this.sessionId)
    return record && record.chatId === this.bridge.chatId ? record : undefined
  }

  get verbosity() {
    const value = this.record?.verbosity ?? this.settings.verbosity
    return VERBOSITY.includes(value) ? value : 'tools'
  }

  get muted() {
    return this.record?.muted === true
  }

  /** Run output work in order for this session, after its topic exists. */
  enqueue(task) {
    const run = this.queueTail.then(async () => {
      await this.bridge.ready
      const threadId = this.record?.threadId ?? (this.bridge.creating.has(this.sessionId) ? await this.bridge.creating.get(this.sessionId) : undefined)
      if (threadId === undefined) return undefined
      return task(threadId)
    })
    this.queueTail = run.catch((error) => this.bridge.report(error, 'session output'))
    return this.queueTail
  }

  closeSegments() {
    const segment = this.text
    if (segment) {
      this.bridge.clearTimer(segment.timer)
      segment.timer = undefined
      segment.sealed = true
      this.flushText(segment, true)
      if (segment.attemptKey !== undefined) {
        this.sealed.set(segment.attemptKey, segment)
        // Only the latest few attempts can still be live.
        while (this.sealed.size > 4) this.sealed.delete(this.sealed.keys().next().value)
      }
    }
    this.text = undefined
    this.tools = undefined
  }

  userMessage(text, images) {
    this.closeSegments()
    if (this.muted) return undefined
    const suffix = images ? ` 🖼×${images}` : ''
    const body = truncate(String(text ?? ''), 3500)
    return this.enqueue((threadId) => this.bridge.sendHtml(threadId, `👤 <b>You (DSH)</b>${suffix}\n${escapeHtml(body)}`, { priority: PRIORITY.NORMAL }))
  }

  turnStarted() {
    this.running = true
    this.turnStartedAt = Date.now()
    this.lastAssistantText = ''
    this.startTyping()
    this.updateStatus()
  }

  turnEnded({ reason, error, usage } = {}) {
    const duration = this.turnStartedAt ? Date.now() - this.turnStartedAt : 0
    this.running = false
    this.stopTyping()
    const finalText = this.lastAssistantText
    this.closeSegments()
    this.sealed.clear()
    // Calls without a result (aborted turn) and finished subagents are done.
    this.toolIndex.clear()
    for (const [childId, sub] of this.subagents) if (sub.state !== 'running') this.subagents.delete(childId)
    if (this.verbosity === 'final' && !this.muted && finalText) this.sendFinalOnly(finalText)
    this.enqueue(async (threadId) => {
      if (this.muted) return
      let line
      if (reason === 'cancelled' || reason === 'user' || reason === 'interrupted') line = `⏹ Stopped · ${formatDuration(duration)}`
      else if (reason === 'error') line = `⚠️ Turn failed${error ? `: ${escapeHtml(truncate(oneLine(error), 300))}` : ''}`
      else if (this.settings.notifyOnTurnEnd) line = `✅ Done · ${formatDuration(duration)}${usage?.contextTokens ? ` · ctx ${formatTokens(usage.contextTokens)}` : ''}`
      if (line) await this.bridge.sendHtml(threadId, line, { priority: PRIORITY.NORMAL, silent: false })
    })
    this.updateStatus()
  }

  sendFinalOnly(markdown) {
    this.enqueue(async (threadId) => {
      if (splitMarkdown(markdown).length <= 1) {
        for (const html of renderMarkdownMessages(markdown)) await this.bridge.sendHtml(threadId, html, { plain: markdownToPlain(markdown) })
        return
      }
      await this.bridge.sendHtml(threadId, previewHtml(markdown))
      await this.sendMarkdownFile(threadId, markdown)
    })
  }

  startTyping() {
    if (!this.settings.typingIndicator || this.typingTimer !== undefined) return
    const tick = () => {
      if (!this.running) {
        this.typingTimer = undefined
        return
      }
      const record = this.record
      if (record && !this.muted) this.bridge.send('sendChatAction', { chat_id: this.bridge.chatId, message_thread_id: record.threadId, action: 'typing' }, { priority: PRIORITY.LOW, key: `typing:${this.sessionId}` }).catch(() => undefined)
      this.typingTimer = this.bridge.setTimer(tick, 9000)
    }
    tick()
  }

  stopTyping() {
    this.bridge.clearTimer(this.typingTimer)
    this.typingTimer = undefined
  }

  async statusHtml() {
    const info = (await this.bridge.host.sessionInfo(this.sessionId)) ?? {}
    const stats = (await this.bridge.host.sessionStats?.(this.sessionId)) ?? {}
    const lines = [`🧵 <b>${escapeHtml(info.workspaceTitle ?? 'workspace')}</b> · ${escapeHtml(info.title ?? 'session')}`]
    if (info.cwd) lines.push(`<code>${escapeHtml(info.cwd)}</code>`)
    lines.push(`State: ${this.running ? '⏳ working' : '💤 idle'}${this.muted ? ' · 🔇 muted' : ''}`)
    if (info.model) lines.push(`Model: <b>${escapeHtml(info.model)}</b>`)
    const permissions = await this.bridge.host.permissions?.(this.sessionId)?.catch?.(() => undefined)
    if (permissions?.current) lines.push(`Access: <b>${escapeHtml(permissionLabel(permissions.options.find((option) => option.value === permissions.current) ?? permissions.current))}</b>`)
    if (stats.contextTokens) lines.push(`Context: ${formatTokens(stats.contextTokens)}${stats.contextWindow ? ` / ${formatTokens(stats.contextWindow)}` : ''}`)
    lines.push(`Output: ${escapeHtml(this.verbosity)}`)
    if (this.subagents.size) {
      const active = [...this.subagents.values()].filter((sub) => sub.state === 'running').length
      if (active) lines.push(`Subagents running: ${active}`)
    }
    return lines.join('\n')
  }

  updateStatus() {
    const record = this.record
    if (!record?.statusMessageId || !this.bridge.settings().pinStatus) return
    this.statusHtml().then(
      (html) => this.bridge.editHtml(record.statusMessageId, html, { priority: PRIORITY.LOW, key: `status:${this.sessionId}` }),
    ).catch((error) => this.bridge.report(error, 'status'))
  }

  // assistant text --------------------------------------------------------

  openText(attemptKey) {
    if (this.text && this.text.attemptKey === attemptKey) return this.text
    if (this.text) this.closeSegments()
    this.tools = undefined
    const sealed = attemptKey === undefined ? undefined : this.sealed.get(attemptKey)
    if (sealed && !sealed.final) {
      // The same attempt continues after a user message: reuse its messages.
      this.sealed.delete(attemptKey)
      sealed.sealed = false
      this.text = sealed
      return sealed
    }
    this.text = { id: ++this.segmentSeq, attemptKey, markdown: '', messageIds: [], rendered: [], final: false, sealed: false, lastFlushAt: 0, timer: undefined }
    return this.text
  }

  assistantDelta(attemptKey, text) {
    if (this.muted || this.verbosity === 'final' || !this.settings.streaming) return
    if (!String(text ?? '').trim()) return
    const segment = this.openText(attemptKey)
    if (segment.final) return
    segment.markdown = text
    const interval = Math.max(100, Number(this.settings.streamIntervalMs) || 2500)
    const wait = segment.lastFlushAt + interval - Date.now()
    if (segment.timer !== undefined) return
    if (wait <= 0) this.flushText(segment, false)
    else segment.timer = this.bridge.setTimer(() => {
      segment.timer = undefined
      if (!segment.final) this.flushText(segment, false)
    }, wait)
  }

  assistantMessage(attemptKey, text, toolCalls) {
    const clean = String(text ?? '').trim()
    if (clean) this.lastAssistantText = clean
    if (!this.muted && this.verbosity !== 'final') {
      if (clean) {
        let segment
        if (this.text && (this.text.attemptKey === attemptKey || attemptKey === undefined)) segment = this.text
        else if (attemptKey !== undefined && this.sealed.has(attemptKey)) {
          // Streamed earlier, then a user message sealed it: finish those messages.
          segment = this.sealed.get(attemptKey)
          if (this.text) this.closeSegments()
          this.tools = undefined
        } else segment = this.openText(attemptKey ?? `final-${this.segmentSeq + 1}`)
        if (attemptKey !== undefined) this.sealed.delete(attemptKey)
        segment.markdown = clean
        segment.final = true
        this.bridge.clearTimer(segment.timer)
        segment.timer = undefined
        this.flushText(segment, true)
        this.text = undefined
      } else if (this.text) {
        this.closeSegments()
      }
    }
    if (attemptKey !== undefined) this.sealed.delete(attemptKey)
    for (const call of toolCalls) this.toolCall(call)
  }

  /**
   * Send or edit the one message of a text segment. A text longer than one
   * message streams its head; once final it becomes a short preview plus
   * the whole answer as a Markdown file. Edits are keyed so flushes coalesce.
   */
  flushText(segment, final) {
    segment.lastFlushAt = Date.now()
    this.enqueue(async (threadId) => {
      if (segment.abandoned) return
      const chunks = splitMarkdown(segment.markdown)
      if (chunks.length === 0) return
      const long = chunks.length > 1
      const done = segment.final
      let html
      if (long && done) html = previewHtml(segment.markdown)
      else {
        html = renderMarkdownMessages(chunks[0])[0] ?? escapeHtml(chunks[0])
        if (long) html += '\n…'
      }
      const shown = !done && !segment.sealed ? `${html} ▍` : html
      if (segment.rendered[0] !== shown) {
        const plain = markdownToPlain(long ? chunks[0] : segment.markdown)
        const messageId = segment.messageIds[0]
        if (messageId === undefined) {
          const message = await this.bridge.sendHtml(threadId, shown, { priority: PRIORITY.NORMAL, plain })
          segment.messageIds[0] = message?.message_id
        } else {
          await this.bridge.editHtml(messageId, shown, { priority: final ? PRIORITY.NORMAL : PRIORITY.LOW, key: `text:${this.sessionId}:${segment.id}`, plain })
        }
        segment.rendered[0] = shown
      }
      if (long && done && !segment.fileSent) {
        segment.fileSent = true
        await this.sendMarkdownFile(threadId, segment.markdown)
      }
    })
  }

  /** The whole answer as `answer.md`; if Telegram refuses the file, as messages. */
  async sendMarkdownFile(threadId, markdown) {
    const document = new File([markdown], LONG_TEXT_FILE, { type: 'text/markdown' })
    try {
      await this.bridge.send('sendDocument', { chat_id: this.bridge.chatId, message_thread_id: threadId, document, disable_notification: true }, { priority: PRIORITY.NORMAL })
    } catch (error) {
      this.bridge.report(error, 'answer file')
      for (const html of renderMarkdownMessages(markdown)) await this.bridge.sendHtml(threadId, html, { priority: PRIORITY.NORMAL })
    }
  }

  abandonText(attemptKey) {
    const segment = this.text?.attemptKey === attemptKey ? this.text : this.sealed.get(attemptKey)
    if (!segment || segment.final) return
    this.bridge.clearTimer(segment.timer)
    segment.timer = undefined
    segment.final = true
    segment.abandoned = true
    this.sealed.delete(attemptKey)
    if (this.text === segment) this.text = undefined
    this.enqueue(async () => {
      for (const messageId of segment.messageIds) {
        if (messageId) await this.bridge.send('deleteMessage', { chat_id: this.bridge.chatId, message_id: messageId }, { priority: PRIORITY.LOW }).catch(() => undefined)
      }
      segment.messageIds = []
    })
  }

  // tools -----------------------------------------------------------------

  toolCall(call) {
    if (this.verbosity === 'final' || this.muted) return
    if (!this.tools) {
      this.tools = { id: ++this.segmentSeq, calls: [], messageId: undefined }
    }
    // Keep only what the block renders; tool inputs (file contents) can be large.
    const entry = { id: call.id, name: call.name, input: compactInput(call.input), state: 'running' }
    this.tools.calls.push(entry)
    if (call.id) this.toolIndex.set(call.id, { segment: this.tools, entry })
    this.flushTools(this.tools)
  }

  toolResult(callId, isError, text) {
    const found = this.toolIndex.get(callId)
    if (!found) return
    this.toolIndex.delete(callId)
    found.entry.state = isError ? 'error' : 'ok'
    if (isError) found.entry.error = text === undefined ? undefined : truncate(String(text), 400)
    this.flushTools(found.segment)
  }

  flushTools(segment) {
    const collapsed = this.verbosity !== 'full'
    this.enqueue(async (threadId) => {
      let html = toolBlock(segment.calls)
      if (!collapsed) html = html.replace('<blockquote expandable>', '<blockquote>')
      // The session chain runs one task at a time, so a failed send is simply retried by the next flush.
      if (segment.messageId === undefined) {
        const message = await this.bridge.sendHtml(threadId, html, { priority: PRIORITY.NORMAL })
        segment.messageId = message?.message_id
        segment.rendered = html
        return
      }
      if (segment.rendered === html) return
      segment.rendered = html
      await this.bridge.editHtml(segment.messageId, html, { priority: PRIORITY.LOW, key: `tools:${this.sessionId}:${segment.id}` })
    })
  }

  // subagents -------------------------------------------------------------

  subagentStarted(childId, info = {}) {
    if (this.subagents.has(childId)) return
    const sub = { ...info, state: 'running', startedAt: Date.now(), messageId: undefined }
    this.subagents.set(childId, sub)
    if (this.muted) return
    // A subagent line splits the tool block so the line keeps its place.
    this.tools = undefined
    if (this.text) this.closeSegments()
    sub.sent = this.enqueue(async (threadId) => {
      const message = await this.bridge.sendHtml(threadId, subagentLine(sub), { priority: PRIORITY.NORMAL })
      sub.messageId = message?.message_id
    })
    this.updateStatus()
  }

  subagentLabel(childId, label) {
    const sub = this.subagents.get(childId)
    if (!sub || sub.description || !label) return
    sub.description = label
    if (this.muted) return
    this.enqueue(async () => {
      await sub.sent
      if (sub.messageId) await this.bridge.editHtml(sub.messageId, subagentLine(sub), { priority: PRIORITY.LOW, key: `sub:${childId}` })
    })
  }

  subagentEnded(childId, info = {}) {
    const sub = this.subagents.get(childId)
    if (!sub || sub.state !== 'running') return
    sub.state = info.state ?? 'done'
    sub.durationMs = Date.now() - sub.startedAt
    if (info.description && !sub.description) sub.description = info.description
    this.updateStatus()
    if (this.muted) return
    this.enqueue(async () => {
      await sub.sent
      if (sub.messageId) await this.bridge.editHtml(sub.messageId, subagentLine(sub), { priority: PRIORITY.LOW, key: `sub:${childId}` })
    })
  }

  // files -----------------------------------------------------------------

  sendFiles(files) {
    if (this.muted) return
    this.enqueue(async (threadId) => {
      for (const file of files) {
        try {
          const blob = await this.bridge.host.readFile(this.sessionId, file.path)
          await this.bridge.send('sendDocument', { chat_id: this.bridge.chatId, message_thread_id: threadId, document: blob, caption: file.description ? truncate(file.description, 1000) : undefined, disable_notification: true }, { priority: PRIORITY.NORMAL })
        } catch (error) {
          await this.bridge.sendHtml(threadId, `📎 Cannot send <code>${escapeHtml(file.path)}</code>: ${escapeHtml(error?.message ?? String(error))}`)
        }
      }
    })
  }

  // inbound ---------------------------------------------------------------

  async inbound(message, text) {
    const bridge = this.bridge
    const threadId = message.message_thread_id
    const reply = (html, options = {}) => bridge.sendHtml(threadId, html, { replyTo: message.message_id, priority: PRIORITY.HIGH, ...options })
    const command = /^\/(\w+)(?:@\w+)?(?:\s+([\s\S]*))?$/.exec(text.trim())
    if (command && !message.photo && !message.document && !message.voice) return this.command(command[1].toLowerCase(), (command[2] ?? '').trim(), message, reply)
    if (this.pendingQuestion && text && !message.photo && !message.document && !message.voice) {
      if (this.pendingQuestion.answerText(text)) return undefined
    }
    let mode = 'followup'
    let body = text
    const prefix = this.settings.steerPrefix
    if (prefix && body.startsWith(prefix)) {
      mode = 'steer'
      body = body.slice(prefix.length).trimStart()
    }
    let input
    try {
      input = await this.buildInput(message, body)
    } catch (error) {
      return reply(`⚠️ ${escapeHtml(error?.message ?? String(error))}`)
    }
    if (!input) return undefined
    try {
      await bridge.host.sendInput(this.sessionId, input, { mode, telegram: { messageId: message.message_id, userId: message.from?.id } })
      return undefined
    } catch (error) {
      return reply(`⚠️ Not delivered: ${escapeHtml(error?.message ?? String(error))}`)
    }
  }

  /** Text plus downloaded media as a host input. */
  async buildInput(message, text) {
    const bridge = this.bridge
    const maxBytes = (Number(this.settings.maxDownloadMb) || 20) * 1024 * 1024
    const attachments = []
    let transcript
    const fetchFile = async (fileId, size) => {
      if (size && size > maxBytes) throw new Error(`file is larger than ${this.settings.maxDownloadMb} MB`)
      const file = await bridge.api.call('getFile', { file_id: fileId })
      return bridge.api.download(file.file_path, { maxBytes })
    }
    if (message.photo?.length) {
      const photo = message.photo.at(-1)
      attachments.push({ kind: 'image', data: await fetchFile(photo.file_id, photo.file_size), mime: 'image/jpeg', name: `photo-${message.message_id}.jpg` })
    }
    if (message.document) {
      const doc = message.document
      const mime = doc.mime_type ?? 'application/octet-stream'
      attachments.push({ kind: mime.startsWith('image/') ? 'image' : 'file', data: await fetchFile(doc.file_id, doc.file_size), mime, name: doc.file_name ?? `file-${message.message_id}` })
    }
    const voice = message.voice ?? message.audio ?? message.video_note
    if (voice) {
      const data = await fetchFile(voice.file_id, voice.file_size)
      const mime = voice.mime_type ?? (message.video_note ? 'video/mp4' : 'audio/ogg')
      if (this.settings.voice && bridge.host.transcribe) {
        try {
          transcript = await bridge.host.transcribe(data, mime)
        } catch (error) {
          bridge.report(error, 'transcribe')
        }
      }
      if (transcript) {
        await bridge.sendHtml(message.message_thread_id, `🎙 <i>${escapeHtml(truncate(transcript, 3500))}</i>`, { replyTo: message.message_id, priority: PRIORITY.NORMAL })
      } else {
        attachments.push({ kind: 'file', data, mime, name: `voice-${message.message_id}.${mime.includes('ogg') ? 'ogg' : 'bin'}` })
      }
    }
    const parts = []
    if (transcript) parts.push(text ? `${text}\n\n${transcript}` : transcript)
    else if (text) parts.push(text)
    const finalText = parts.join('\n').trim()
    if (!finalText && attachments.length === 0) return undefined
    return { text: finalText, attachments }
  }

  async command(name, args, message, reply) {
    const bridge = this.bridge
    const host = bridge.host
    try {
      switch (name) {
        case 'help':
        case 'start':
          return reply(TOPIC_HELP)
        case 'stop':
        case 'cancel': {
          const stopped = await host.cancel(this.sessionId)
          return reply(stopped === false ? 'Nothing is running.' : '⏹ Stop requested.')
        }
        case 'status':
          return reply(await this.statusHtml())
        case 'steer':
          if (!args) return reply('Usage: /steer &lt;text&gt;')
          await host.sendInput(this.sessionId, { text: args, attachments: [] }, { mode: 'steer', telegram: { messageId: message.message_id } })
          return undefined
        case 'diff': {
          const diff = await host.diff(this.sessionId)
          if (!diff || (!diff.stat && !diff.patch)) return reply('No changes.')
          const stat = diff.stat ? `<pre>${escapeHtml(truncate(diff.stat, 3000))}</pre>` : ''
          await reply(`🧾 <b>Changes</b>\n${stat}`)
          if (diff.patch) {
            const blob = new Blob([diff.patch], { type: 'text/x-diff' })
            blob.name = 'changes.diff'
            await bridge.send('sendDocument', { chat_id: bridge.chatId, message_thread_id: message.message_thread_id, document: Object.assign(blob, { name: 'changes.diff' }), disable_notification: true })
          }
          return undefined
        }
        case 'compact': {
          await reply('🗜 Compacting…')
          const result = await host.compact(this.sessionId)
          return reply(result ? `🗜 ${escapeHtml(result)}` : '🗜 Compaction finished.')
        }
        case 'model': {
          if (!args) {
            const info = (await host.sessionInfo(this.sessionId)) ?? {}
            const models = (await host.listModels?.()) ?? []
            const keyboard = models.slice(0, 24).map((model) => [{ text: `${model.label}${model.id === info.modelId ? ' ✓' : ''}`, callback_data: `cmd:${this.modelCallback()}:${models.indexOf(model)}` }])
            this.modelChoices = models
            return reply(`Model: <b>${escapeHtml(info.model ?? 'unknown')}</b>${models.length ? '\nPick one:' : ''}`, { replyMarkup: keyboard.length ? { inline_keyboard: keyboard } : undefined })
          }
          const label = await host.setModel(this.sessionId, args)
          this.updateStatus()
          return reply(`Model set to <b>${escapeHtml(label ?? args)}</b> for the next turn.`)
        }
        case 'permission':
        case 'permissions':
        case 'access': {
          const permissions = await host.permissions?.(this.sessionId)
          if (!permissions?.options?.length) return reply('⚠️ Permission presets are not available in this DSH.')
          if (!args) {
            const current = permissions.options.find((option) => option.value === permissions.current)
            const keyboard = permissions.options.map((option) => [{ text: `${permissionLabel(option)}${option.value === permissions.current ? ' ✓' : ''}`, callback_data: `cmd:${this.permissionCallback()}:${option.value}` }])
            return reply(`🔐 Access: <b>${escapeHtml(current ? permissionLabel(current) : permissions.current ?? 'unknown')}</b>\nPick one:`, { replyMarkup: { inline_keyboard: keyboard } })
          }
          const value = resolvePermission(permissions.options, args)
          if (!value) return reply(`Unknown access <code>${escapeHtml(args)}</code>. Use: ${permissions.options.map((option) => `<code>${escapeHtml(option.value)}</code>`).join(', ')}`)
          await host.setPermission(this.sessionId, value)
          return reply(`🔐 Access set to <b>${escapeHtml(permissionLabel(permissions.options.find((option) => option.value === value)))}</b>`)
        }
        case 'verbosity':
        case 'mode': {
          const value = args.toLowerCase()
          if (!VERBOSITY.includes(value)) return reply(`Output detail: <b>${this.verbosity}</b>\n/verbosity full — every tool expanded\n/verbosity tools — tools collapsed (default)\n/verbosity final — final answers only`)
          await bridge.store.patchTopic(this.sessionId, { verbosity: value })
          this.updateStatus()
          return reply(`Output detail: <b>${value}</b>`)
        }
        case 'mute':
          await bridge.store.patchTopic(this.sessionId, { muted: true })
          this.updateStatus()
          return reply('🔇 Agent output is no longer mirrored here. Approvals and questions still arrive. /unmute to resume.')
        case 'unmute':
          await bridge.store.patchTopic(this.sessionId, { muted: false })
          this.updateStatus()
          return reply('🔊 Mirroring resumed.')
        default: {
          // DSH slash commands (/goal, /permission, plugin commands) run like in the composer.
          const ran = await host.runCommand?.(this.sessionId, String(message.text ?? '').replace(/^\/(\w+)@\w+/, '/$1').trim())
          if (ran) return reply(`${ran.ok ? '⚙️' : '⚠️'} /${escapeHtml(name)}${ran.text ? `\n${escapeHtml(truncate(ran.text, 3500))}` : ' done.'}`)
          // Otherwise the text goes to the agent as-is.
          await host.sendInput(this.sessionId, { text: message.text, attachments: [] }, { mode: 'followup', telegram: { messageId: message.message_id } })
          return undefined
        }
      }
    } catch (error) {
      return reply(`⚠️ /${escapeHtml(name)} failed: ${escapeHtml(error?.message ?? String(error))}`)
    }
  }

  permissionCallback() {
    if (this.permissionCallbackId && this.bridge.callbacks.has(this.permissionCallbackId)) return this.permissionCallbackId
    this.permissionCallbackId = this.bridge.registerCallback({
      kind: 'permission',
      sessionId: this.sessionId,
      onPress: async (value) => {
        const permissions = await this.bridge.host.permissions?.(this.sessionId)
        const option = permissions?.options?.find((entry) => entry.value === value)
        if (!option) return 'Unknown access mode'
        await this.bridge.host.setPermission(this.sessionId, value)
        const record = this.record
        if (record) this.bridge.sendHtml(record.threadId, `🔐 Access set to <b>${escapeHtml(permissionLabel(option))}</b>`).catch(() => undefined)
        return `Access: ${permissionLabel(option)}`
      },
    })
    return this.permissionCallbackId
  }

  modelCallback() {
    if (this.modelCallbackId && this.bridge.callbacks.has(this.modelCallbackId)) return this.modelCallbackId
    this.modelCallbackId = this.bridge.registerCallback({
      kind: 'model',
      sessionId: this.sessionId,
      onPress: async (action) => {
        const model = this.modelChoices?.[Number(action)]
        if (!model) return 'Unknown model'
        const label = await this.bridge.host.setModel(this.sessionId, model.id)
        this.updateStatus()
        const record = this.record
        if (record) this.bridge.sendHtml(record.threadId, `Model set to <b>${escapeHtml(label ?? model.label)}</b> for the next turn.`).catch(() => undefined)
        return `Model: ${label ?? model.label}`
      },
    })
    return this.modelCallbackId
  }
}
