/**
 * DSH Telegram forum bridge.
 *
 * Every root DSH session gets its own topic in one Telegram forum
 * supergroup. What is typed in DSH and what the agent outputs appears in the
 * topic; what is written in the topic goes into the session. Subagents never
 * get topics: they show as one status line in their root session's topic.
 * Approvals and ask_user_question prompts appear with inline buttons and race
 * the GUI: whichever side answers first wins and the other is withdrawn.
 */
import { isAbsolute, join, dirname } from 'node:path'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { Bridge, DEFAULT_SETTINGS, VERBOSITY } from './lib/bridge.js'
import { BridgeStore, DOMAIN, memoryDomain } from './lib/store.js'
import { approvalPreview, createHost, isRootSession, wireEvents } from './lib/host.js'
import { createBotApi } from './lib/telegram/api.js'
import { SendQueue } from './lib/telegram/queue.js'
import { runPoller } from './lib/telegram/poller.js'

export const name = 'telegram-bridge'
/** Required services; optional ones are read lazily with ctx.get(). */
export const inject = ['storageDomain', 'sessions', 'agents', 'sessionController']

export const ENTRY_ID = 'telegram-bridge'
export const DEFAULT_TOKEN_REF = 'DSH_TELEGRAM_BOT_TOKEN'
export const ROUTES = Object.freeze({
  status: '/telegram-bridge/status',
  test: '/telegram-bridge/test',
  token: '/telegram-bridge/token',
  bind: '/telegram-bridge/bind',
  unbind: '/telegram-bridge/unbind',
  settings: '/telegram-bridge/settings',
  session: '/telegram-bridge/session',
})

export const DEFAULTS = Object.freeze({
  ...DEFAULT_SETTINGS,
  enabled: true,
  tokenRef: DEFAULT_TOKEN_REF,
  perMinute: 20,
  apiBaseUrl: 'https://api.telegram.org',
  transcribeCommand: '',
  transcribeLanguage: '',
  ffmpegPath: 'ffmpeg',
})

/** Every field is volatile: the settings page edits them live, without a remount. */
export function buildConfig(z) {
  const D = DEFAULTS
  const oneOf = (...values) => z.union(values.map((v) => z.const(v)))
  const bool = (v) => z.boolean().default(v).volatile()
  const num = (v, min, max) => z.number().min(min).max(max).default(v).volatile()
  const str = (v) => z.string().default(v).volatile()
  return z.object({
    enabled: bool(D.enabled),
    tokenRef: str(D.tokenRef),
    chatId: z.number().default(D.chatId).volatile(),
    allowedUserIds: z.array(Number).default([]).volatile(),
    verbosity: oneOf(...VERBOSITY).default(D.verbosity).volatile(),
    streaming: bool(D.streaming),
    streamIntervalMs: num(D.streamIntervalMs, 800, 60000),
    notifyOnTurnEnd: bool(D.notifyOnTurnEnd),
    typingIndicator: bool(D.typingIndicator),
    mirrorGuiMessages: bool(D.mirrorGuiMessages),
    createTopicOn: oneOf('first-message', 'agent-created', 'never').default(D.createTopicOn).volatile(),
    onSessionArchived: oneOf('close', 'delete', 'keep').default(D.onSessionArchived).volatile(),
    approvals: bool(D.approvals),
    questions: bool(D.questions),
    voice: bool(D.voice),
    transcribeCommand: str(D.transcribeCommand),
    transcribeLanguage: str(D.transcribeLanguage),
    ffmpegPath: str(D.ffmpegPath),
    sendPresentedFiles: bool(D.sendPresentedFiles),
    maxDownloadMb: num(D.maxDownloadMb, 1, 20),
    steerPrefix: str(D.steerPrefix),
    pinStatus: bool(D.pinStatus),
    perMinute: num(D.perMinute, 5, 60),
    apiBaseUrl: str(D.apiBaseUrl),
  })
}

/** Schemastery from the running DSH installation (a linked plugin has no dependencies). */
async function loadSchemastery() {
  for (const arg of process.argv.slice(1)) {
    if (typeof arg !== 'string' || !isAbsolute(arg)) continue
    const base = /\.[cm]?js$/.test(arg) ? arg : join(arg, 'package.json')
    try {
      const manifest = createRequire(base).resolve('@deepseek-ai/schemastery/package.json')
      const mod = await import(pathToFileURL(join(dirname(manifest), 'lib', 'index.mjs')).href)
      const z = mod.default ?? mod.Schema
      if (typeof z?.object === 'function') return z
    } catch {
      // next candidate
    }
  }
  return undefined
}

const schemastery = await loadSchemastery()
export const Config = schemastery === undefined ? undefined : buildConfig(schemastery)

const isPlainObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)
const isRef = (v) => isPlainObject(v) && typeof v.get === 'function'
function plain(v) {
  if (isRef(v)) return plain(v.get())
  if (Array.isArray(v)) return v.map(plain)
  if (isPlainObject(v)) return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, plain(x)]))
  return v
}

/** Effective configuration: defaults plus the (volatile-unwrapped) raw config. */
export function resolveConfig(config) {
  const raw = plain(config ?? {})
  const out = { ...DEFAULTS }
  for (const [key, value] of Object.entries(raw)) if (value !== undefined && value !== null && key in DEFAULTS) out[key] = value
  out.chatId = Number(out.chatId) || 0
  out.allowedUserIds = (Array.isArray(out.allowedUserIds) ? out.allowedUserIds : []).map(Number).filter(Number.isSafeInteger)
  if (!VERBOSITY.includes(out.verbosity)) out.verbosity = DEFAULTS.verbosity
  out.allowedUserIds = out.allowedUserIds.filter((id) => id > 0)
  // The token reference names a Telegram credential only, so a settings
  // change cannot make the plugin read (and send to apiBaseUrl) another secret.
  if (!TOKEN_REF.test(out.tokenRef)) out.tokenRef = DEFAULT_TOKEN_REF
  if (!isSafeApiUrl(out.apiBaseUrl)) out.apiBaseUrl = DEFAULTS.apiBaseUrl
  return out
}

const TOKEN_REF = /^[A-Za-z0-9_]*TELEGRAM[A-Za-z0-9_]*$/i

/** https anywhere; plain http only to this machine (a local Bot API server). */
function isSafeApiUrl(value) {
  try {
    const url = new URL(String(value))
    if (url.username || url.password) return false
    if (url.protocol === 'https:') return true
    return url.protocol === 'http:' && ['127.0.0.1', 'localhost', '[::1]'].includes(url.hostname)
  } catch {
    return false
  }
}

/** Settings the HTTP route may change; credentials wiring stays config-file only. */
const ROUTE_SETTABLE = new Set(Object.keys(DEFAULTS).filter((key) => !['tokenRef', 'apiBaseUrl'].includes(key)))

function sendJson(res, status, body) {
  res.statusCode = status
  res.setHeader('content-type', 'application/json; charset=utf-8')
  res.setHeader('cache-control', 'no-store')
  res.end(JSON.stringify(body))
}

async function readBody(req, limit = 65536) {
  let size = 0
  const chunks = []
  for await (const chunk of req) {
    size += chunk.length
    if (size > limit) throw new Error('request body too large')
    chunks.push(chunk)
  }
  const text = Buffer.concat(chunks).toString('utf8')
  if (!text) return {}
  const body = JSON.parse(text)
  if (!isPlainObject(body)) throw new Error('request body must be a JSON object')
  return body
}

const message = (error) => String(error?.message ?? error)

/** Commands shown in Telegram's command menu inside the group. */
export const BOT_COMMANDS = [
  { command: 'help', description: 'Help for this topic' },
  { command: 'status', description: 'Session state, model and context' },
  { command: 'stop', description: 'Stop the running turn' },
  { command: 'diff', description: 'Uncommitted changes' },
  { command: 'compact', description: 'Compact the conversation' },
  { command: 'model', description: 'Show or switch the model' },
  { command: 'verbosity', description: 'Output detail: full, tools, final' },
  { command: 'mute', description: 'Pause mirroring in this topic' },
  { command: 'unmute', description: 'Resume mirroring in this topic' },
  { command: 'sessions', description: 'Recent sessions (General)' },
  { command: 'new', description: 'New session: /new <workspace> [prompt]' },
  { command: 'workspaces', description: 'Known workspaces' },
]

export function apply(ctx, config) {
  const logger = ctx.logger ?? console
  const log = {
    info: (...args) => logger.info?.(...args),
    warn: (...args) => logger.warn?.(...args),
    error: (...args) => logger.error?.(...args),
  }
  const current = () => resolveConfig(config)
  let token
  let settingsService
  let domain = null
  const store = new BridgeStore(memoryDomain())
  let storageReady
  const ready = new Promise((resolve) => { storageReady = resolve })
  let restartPolling = () => {}
  const state = { poller: 'idle', pollerError: undefined, bot: undefined, chat: undefined, lastError: undefined }

  const api = createBotApi({ token: () => token, baseUrl: () => current().apiBaseUrl })
  const queue = new SendQueue({
    perMinute: current().perMinute,
    onError: (error) => {
      state.lastError = message(error)
      log.warn('telegram-bridge: send failed: %s', message(error))
    },
  })
  ctx.effect(() => () => queue.close(), 'telegram-bridge: send queue')

  async function refreshToken() {
    const credentials = ctx.get('credentials')
    if (!credentials) return token
    try {
      const resolved = await credentials.resolve(current().tokenRef)
      token = resolved?.value || undefined
    } catch (error) {
      log.warn('telegram-bridge: cannot read token: %s', message(error))
    }
    return token
  }

  async function writeSettings(patch) {
    if (!settingsService) throw new Error('settings service unavailable')
    const form = settingsService.describe?.().find((row) => row.ns === ENTRY_ID)
    if (!form) throw new Error(`settings entry ${ENTRY_ID} not found`)
    const ops = Object.entries(patch).map(([key, value]) => ({ op: 'set', path: [key], value }))
    await settingsService.mutate(ENTRY_ID, ops, form.revision)
  }

  // Bindings written by /bind are kept here too, so they apply before the settings write lands.
  const overrides = {}
  const settings = () => ({ ...current(), ...overrides })

  const host = createHost(ctx, settings)
  const bridge = new Bridge({
    api,
    queue,
    store,
    host,
    settings,
    ready,
    logger: { info: (m) => log.info('%s', m), warn: (m) => log.warn('%s', m), error: (m) => log.error('%s', m) },
    saveBinding: async (binding) => {
      Object.assign(overrides, binding)
      try {
        await writeSettings(binding)
      } catch (error) {
        log.warn('telegram-bridge: cannot save binding: %s', message(error))
      }
      restartPolling()
    },
  })
  ctx.effect(() => () => bridge.close(), 'telegram-bridge: bridge')
  let active = true
  ctx.effect(() => () => { active = false }, 'telegram-bridge: active')
  const events = wireEvents(ctx, () => (active && settings().enabled ? bridge : undefined))

  // ── storage ───────────────────────────────────────────────────────────
  ctx.effect(() => {
    let disposed = false
    ctx.storageDomain.open(DOMAIN).then(
      (opened) => {
        if (disposed) return opened.close()
        domain = opened
        store.domain = opened
        storageReady()
        return undefined
      },
      (error) => {
        // Keep working from memory; topics are then recreated after a restart.
        log.error('telegram-bridge: cannot open storage domain: %s', message(error))
        storageReady()
      },
    )
    return () => {
      disposed = true
      const open = domain
      domain = null
      store.domain = memoryDomain()
      return open?.close()
    }
  }, 'telegram-bridge: storage')

  ctx.inject(['settings'], (scope) => {
    settingsService = scope.settings
    scope.effect(() => {
      try {
        return scope.settings.configure({ auto: false }, ctx.fiber)
      } catch {
        return () => {}
      }
    }, 'telegram-bridge: settings presentation')
    scope.effect(() => () => { settingsService = undefined }, 'telegram-bridge: settings ref')
  })

  ctx.on('loader/volatile-update', () => {
    // Settings saved: drop local overrides that the config now carries.
    const cfg = current()
    if (overrides.chatId === cfg.chatId) delete overrides.chatId
    if (overrides.allowedUserIds && JSON.stringify(overrides.allowedUserIds) === JSON.stringify(cfg.allowedUserIds)) delete overrides.allowedUserIds
    queue.perMinute = cfg.perMinute
    restartPolling()
  })
  ctx.on('credentials/reference-updated', (ref) => {
    if (ref === current().tokenRef) restartPolling()
  })

  async function setupChat() {
    const chatId = settings().chatId
    if (!token || !chatId) return
    await api.call('setMyCommands', { commands: BOT_COMMANDS, scope: { type: 'chat', chat_id: chatId } }).catch((error) => log.warn('telegram-bridge: setMyCommands: %s', message(error)))
  }

  async function testConnection() {
    await refreshToken()
    if (!token) return { ok: false, code: 'no-token', message: 'Bot token is not set.' }
    const result = { ok: true }
    try {
      const me = await api.call('getMe')
      state.bot = { id: me.id, username: me.username, name: me.first_name }
      result.bot = state.bot
    } catch (error) {
      return { ok: false, code: 'token', message: message(error) }
    }
    const chatId = settings().chatId
    if (chatId) {
      try {
        const chat = await api.call('getChat', { chat_id: chatId })
        state.chat = { id: chat.id, title: chat.title, isForum: chat.is_forum === true }
        result.chat = state.chat
        const member = await api.call('getChatMember', { chat_id: chatId, user_id: state.bot.id })
        result.admin = member.status === 'administrator' || member.status === 'creator'
        result.canManageTopics = member.status === 'creator' || member.can_manage_topics === true
        result.canPin = member.status === 'creator' || member.can_pin_messages === true
        if (!state.chat.isForum) Object.assign(result, { ok: false, code: 'not-forum', message: 'Enable Topics in the group settings.' })
        else if (!result.canManageTopics) Object.assign(result, { ok: false, code: 'rights', message: 'Make the bot an administrator with "Manage topics".' })
      } catch (error) {
        Object.assign(result, { ok: false, code: 'chat', message: message(error) })
      }
    }
    return result
  }

  // ── poller ────────────────────────────────────────────────────────────
  ctx.effect(() => {
    const controller = new AbortController()
    const signal = controller.signal
    // A settings or token change restarts the polling cycle with fresh values.
    let cycle = new AbortController()
    restartPolling = () => cycle.abort()
    /** Sleep until `ms` pass, the plugin stops, or the cycle restarts; leaves no listeners behind. */
    const pause = (ms) => new Promise((resolve) => {
      const cycleSignal = cycle.signal
      const done = () => {
        clearTimeout(timer)
        signal.removeEventListener('abort', done)
        cycleSignal.removeEventListener('abort', done)
        resolve()
      }
      const timer = setTimeout(done, ms)
      signal.addEventListener('abort', done, { once: true })
      cycleSignal.addEventListener('abort', done, { once: true })
      if (signal.aborted || cycleSignal.aborted) done()
    })
    // Messages of one topic are handled in order (a voice note before the
    // text after it); topics and button presses do not wait for each other.
    const inbound = new Map()
    const dispatch = (update) => {
      const run = () => bridge.handleUpdate(update).catch((error) => log.warn('telegram-bridge: update failed: %s', message(error)))
      const msg = update?.message
      if (!msg) return void run()
      const key = `${msg.chat?.id}:${msg.message_thread_id ?? 0}`
      const next = (inbound.get(key) ?? Promise.resolve()).then(run)
      inbound.set(key, next)
      next.finally(() => { if (inbound.get(key) === next) inbound.delete(key) })
    }
    const loop = (async () => {
      await ready
      let failures = 0
      while (!signal.aborted) {
        try {
          if (cycle.signal.aborted) cycle = new AbortController()
          if (!settings().enabled || !(await refreshToken())) {
            state.poller = settings().enabled ? 'no-token' : 'disabled'
            await pause(15000)
            continue
          }
          await testConnection().catch(() => undefined)
          await setupChat().catch(() => undefined)
          await runPoller({
            api,
            signal: AbortSignal.any([signal, cycle.signal]),
            getOffset: () => store.state('poller')?.offset,
            setOffset: (offset) => store.putState('poller', { offset }),
            onUpdate: async (update) => {
              if (settings().enabled) dispatch(update)
            },
          onState: ({ status, error }) => {
            state.poller = status
            state.pollerError = error ? message(error) : undefined
            if (status === 'conflict') log.warn('telegram-bridge: another process polls this bot (409); stop it or use another bot')
            else if (error) log.warn('telegram-bridge: polling %s: %s', status, message(error))
          },
          })
          failures = 0
        } catch (error) {
          // Never let one failure end polling for the rest of the process.
          failures += 1
          state.poller = 'error'
          state.pollerError = message(error)
          log.warn('telegram-bridge: poller failed: %s', message(error))
          await pause(Math.min(60000, 1000 * 2 ** Math.min(failures, 6)))
        }
      }
    })()
    return async () => {
      restartPolling = () => {}
      controller.abort()
      await loop.catch(() => undefined)
    }
  }, 'telegram-bridge: poller')

  // ── approvals and questions: race Telegram buttons against the GUI ────

  /** Root session that owns this agent's prompts; undefined for an unresolvable subagent. */
  function promptOwner(agent) {
    const root = events.rootOf(agent.id)
    if (root !== undefined) return root
    return isRootSession(agent.session) ? agent.id : undefined
  }

  /**
   * Swap the request signal so aborting `withdraw` takes the GUI card back.
   * A frozen request keeps its signal; the GUI card then stays until answered.
   */
  function attachWithdraw(request) {
    const withdraw = new AbortController()
    const original = request.signal
    try {
      request.signal = original ? AbortSignal.any([original, withdraw.signal]) : withdraw.signal
    } catch {
      // frozen request
    }
    return { withdraw, original }
  }

  /**
   * Race the GUI (`next()`) against a Telegram prompt that is posted in
   * parallel, so the DSH card never waits for Telegram. A GUI result that is
   * not an answer (no client: 'unavailable' or an error) only ends the race
   * once Telegram cannot answer either (prompt not delivered, bridge closed).
   */
  function race({ next, original, decisive, prompt, answerOf }) {
    const gui = Promise.resolve().then(next).then((value) => ({ from: 'gui', value }), (error) => ({ from: 'gui', error }))
    return new Promise((resolve) => {
      let guiResult
      let telegramDead = false
      gui.then((result) => {
        guiResult = result
        if (telegramDead || decisive(result)) resolve(result)
      })
      prompt.then((handle) => (handle ? answerOf(handle) : undefined)).then(
        (value) => {
          if (value !== undefined) return resolve({ from: 'telegram', value })
          telegramDead = true
          if (guiResult) resolve(guiResult)
          return undefined
        },
        () => {
          telegramDead = true
          if (guiResult) resolve(guiResult)
        },
      )
      if (original) {
        const cancel = () => resolve({ from: 'cancel' })
        if (original.aborted) cancel()
        else original.addEventListener('abort', cancel, { once: true })
      }
    })
  }

  ctx.on('approval/request', async function (request, next) {
    const b = active && settings().enabled ? bridge : undefined
    const agent = request?.agent
    if (!b || !agent?.session || !settings().approvals || !settings().chatId) return next()
    const root = promptOwner(agent)
    if (root === undefined) return next()
    const call = request.callId ? events.callArgs(agent.id, request.callId) : undefined
    const { withdraw, original } = attachWithdraw(request)
    const prompt = b.requestApproval(root, {
      toolName: request.toolName,
      reason: request.reason,
      displayReason: request.displayReason?.en ?? (typeof request.displayReason === 'string' ? request.displayReason : undefined),
      preview: approvalPreview(call),
      subagent: root !== agent.id,
    }).catch((error) => {
      log.warn('telegram-bridge: approval prompt failed: %s', message(error))
      return undefined
    })
    const winner = await race({
      next,
      original,
      prompt,
      decisive: (result) => result.value === 'allowed-once' || result.value === 'rejected',
      answerOf: (handle) => handle.answer,
    })
    if (winner.from === 'telegram') withdraw.abort(new Error('answered in Telegram'))
    const outcome = winner.from === 'cancel' ? 'cancelled' : winner.value
    prompt.then((handle) => handle?.settle(winner.error ? undefined : outcome, winner.from === 'gui' ? 'answered in DSH' : winner.from === 'telegram' ? 'answered in Telegram' : undefined))
    if (winner.error) throw winner.error
    return outcome
  }, { prepend: true })

  ctx.on('user-questions/request', async function (request, next) {
    const b = active && settings().enabled ? bridge : undefined
    const agent = request?.agent
    if (!b || !agent?.session || !settings().questions || !settings().chatId || !Array.isArray(request.questions)) return next()
    const root = promptOwner(agent)
    if (root === undefined) return next()
    const { withdraw, original } = attachWithdraw(request)
    const prompt = b.requestQuestions(root, request.questions).catch((error) => {
      log.warn('telegram-bridge: question prompt failed: %s', message(error))
      return undefined
    })
    const winner = await race({
      next,
      original,
      prompt,
      decisive: (result) => result.error === undefined,
      answerOf: (handle) => handle.answer.then((answers) => (answers ? { answers } : undefined)),
    })
    if (winner.from === 'telegram') withdraw.abort(new Error('answered in Telegram'))
    else {
      // A timed question that ran out stays answerable in DSH, not here.
      const via = winner.from === 'gui' ? 'Answered in DSH' : request.wait !== undefined ? 'No answer in time here — answer it in DSH' : 'Cancelled'
      prompt.then((handle) => handle?.cancel(via))
    }
    if (winner.from === 'cancel') throw original?.reason ?? new Error('question cancelled')
    if (winner.error) throw winner.error
    return winner.value
  }, { prepend: true })

  // ── settings page routes ──────────────────────────────────────────────
  ctx.inject(['webServer', 'connection'], (scope) => {
    const route = (methods, handle) => async (req, res) => {
      const rejection = scope.connection.requestRejection(req)
      if (rejection !== undefined) {
        res.statusCode = rejection
        res.end()
        return
      }
      if (!methods.includes(req.method)) {
        res.setHeader('allow', methods.join(', '))
        sendJson(res, 405, { ok: false, message: 'method not allowed' })
        return
      }
      try {
        await handle(req, res)
      } catch (error) {
        if (!res.headersSent) sendJson(res, 200, { ok: false, code: error?.code ?? 'failed', message: message(error) })
      }
    }
    const register = (path, methods, handle) => scope.effect(() => scope.webServer.register({ kind: 'exact', path, handler: route(methods, handle) }), `telegram-bridge: ${path}`)

    register(ROUTES.status, ['GET'], async (_req, res) => {
      const cfg = settings()
      const credentials = ctx.get('credentials')
      let tokenState = { configured: Boolean(token) }
      try {
        if (credentials) tokenState = await credentials.describe(cfg.tokenRef)
      } catch {
        // keep the cached state
      }
      sendJson(res, 200, {
        ok: true,
        enabled: cfg.enabled,
        token: { ref: cfg.tokenRef, configured: tokenState?.configured === true, source: tokenState?.source, writable: tokenState?.writable },
        bot: state.bot,
        chat: state.chat ?? (cfg.chatId ? { id: cfg.chatId } : undefined),
        allowedUserIds: cfg.allowedUserIds,
        poller: state.poller,
        pollerError: state.pollerError,
        lastError: state.lastError,
        topics: store.topics().length,
        queued: queue.size,
        storage: domain !== null,
        bindCode: bridge.bindCode && Date.now() < bridge.bindCode.expiresAt ? bridge.bindCode : undefined,
        settings: Object.fromEntries(Object.entries(cfg).filter(([key]) => !['tokenRef'].includes(key))),
      })
    })

    register(ROUTES.test, ['POST'], async (_req, res) => sendJson(res, 200, await testConnection()))

    register(ROUTES.token, ['POST'], async (req, res) => {
      const body = await readBody(req)
      const value = String(body.token ?? '').trim()
      if (!/^\d{5,}:[A-Za-z0-9_-]{20,}$/.test(value)) return sendJson(res, 200, { ok: false, code: 'format', message: 'This does not look like a bot token (123456:ABC…).' })
      const credentials = ctx.get('credentials')
      if (!credentials) return sendJson(res, 200, { ok: false, code: 'credentials', message: 'Credentials service unavailable.' })
      await credentials.set(current().tokenRef, value)
      token = value
      const result = await testConnection()
      restartPolling()
      return sendJson(res, 200, result)
    })

    register(ROUTES.bind, ['POST'], async (_req, res) => {
      if (!(await refreshToken())) return sendJson(res, 200, { ok: false, code: 'no-token', message: 'Set the bot token first.' })
      const bind = bridge.createBindCode()
      sendJson(res, 200, { ok: true, ...bind, bot: state.bot })
    })

    register(ROUTES.unbind, ['POST'], async (_req, res) => {
      delete overrides.chatId
      await writeSettings({ chatId: 0 })
      Object.assign(overrides, { chatId: 0 })
      sendJson(res, 200, { ok: true })
    })

    register(ROUTES.settings, ['POST'], async (req, res) => {
      const body = await readBody(req)
      const patch = {}
      for (const [key, value] of Object.entries(body.patch ?? {})) if (ROUTE_SETTABLE.has(key)) patch[key] = value
      if (Object.keys(patch).length === 0) return sendJson(res, 200, { ok: false, code: 'empty', message: 'Nothing to change.' })
      await writeSettings(patch)
      for (const key of Object.keys(patch)) delete overrides[key]
      sendJson(res, 200, { ok: true })
    })

    register(ROUTES.session, ['GET'], async (req, res) => {
      const id = new URL(req.url ?? '', 'http://local').searchParams.get('id') ?? ''
      const record = store.topic(id)
      const cfg = settings()
      sendJson(res, 200, {
        ok: true,
        linked: Boolean(record && record.chatId === cfg.chatId),
        link: record && record.chatId === cfg.chatId ? bridge.topicLink(record.threadId) : undefined,
        name: record?.name,
        muted: record?.muted === true,
      })
    })
  })

  return () => {
    active = false
  }
}
