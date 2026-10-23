/** Shared test setup: a Bridge wired to a fake Bot API and a fake DSH host. */
import { Bridge, DEFAULT_SETTINGS } from '../lib/bridge.js'
import { BridgeStore } from '../lib/store.js'
import { createBotApi } from '../lib/telegram/api.js'
import { SendQueue } from '../lib/telegram/queue.js'
import { startFakeTelegram } from './fake-telegram.mjs'

export const TOKEN = '123456789:AAEexampleexampleexampleexampleexa'
export const CHAT = -1001234567890
export const OWNER = 777

export function fakeHost(extra = {}) {
  const calls = []
  const sessions = new Map()
  const host = {
    calls,
    sessions,
    async sessionInfo(id) {
      const info = sessions.get(id)
      return info ? { sessionId: id, ...info } : { sessionId: id }
    },
    async sessionStats() {
      return { contextTokens: 12000, contextWindow: 200000 }
    },
    async sendInput(id, input, options) {
      calls.push({ op: 'sendInput', id, input, options })
      return 'tg-1'
    },
    async cancel(id) {
      calls.push({ op: 'cancel', id })
      return true
    },
    async compact(id) {
      calls.push({ op: 'compact', id })
      return 'Compacted 5000 tokens.'
    },
    async diff(id) {
      calls.push({ op: 'diff', id })
      return { stat: ' a.js | 2 +-', patch: 'diff --git a/a.js b/a.js\n-old\n+new\n' }
    },
    async listModels() {
      return [{ id: 'p/m1', provider: 'p', model: 'm1', label: 'Model One' }, { id: 'p/m2', provider: 'p', model: 'm2', label: 'Model Two' }]
    },
    async setModel(id, target) {
      calls.push({ op: 'setModel', id, target })
      return target === 'p/m2' || target === 'm2' ? 'Model Two' : 'Model One'
    },
    permission: 'workspace-write',
    async permissions() {
      return {
        options: [{ value: 'read-only', name: 'read-only' }, { value: 'workspace-write', name: 'workspace-write' }, { value: 'danger-full-access', name: 'danger-full-access' }],
        current: host.permission,
      }
    },
    async setPermission(id, preset) {
      calls.push({ op: 'setPermission', id, preset })
      host.permission = preset
      return preset
    },
    async listSessions() {
      return [...sessions.entries()].map(([sessionId, info]) => ({ sessionId, title: info.title, workspaceTitle: info.workspaceTitle, running: false }))
    },
    async listWorkspaces() {
      return [{ id: 'ws1', title: 'dsh-tg-bridge', path: '/home/u/dsh-tg-bridge' }]
    },
    async newSession(path, first, options) {
      calls.push({ op: 'newSession', path, first, ...options })
      sessions.set('session-new', { cwd: path, workspaceTitle: 'dsh-tg-bridge', title: '' })
      return 'session-new'
    },
    async readFile(id, path) {
      calls.push({ op: 'readFile', id, path })
      return Object.assign(new Blob(['hello']), { name: 'report.txt' })
    },
    async transcribe(data, mime) {
      calls.push({ op: 'transcribe', size: data.length, mime })
      return 'привет из голосового'
    },
    ...extra,
  }
  return host
}

export async function makeBridge({ settings = {}, host = fakeHost(), bound = true } = {}) {
  const tg = await startFakeTelegram({ chatId: CHAT })
  const api = createBotApi({ token: () => TOKEN, baseUrl: tg.url })
  const queue = new SendQueue({ perMinute: 10000, minGapMs: 0 })
  const store = new BridgeStore()
  const current = {
    ...DEFAULT_SETTINGS,
    chatId: bound ? CHAT : 0,
    allowedUserIds: bound ? [OWNER] : [],
    streamIntervalMs: 100,
    typingIndicator: false,
    ...settings,
  }
  const bindings = []
  const bridge = new Bridge({
    api,
    queue,
    store,
    host,
    settings: () => current,
    logger: { info() {}, warn() {}, error() {} },
    saveBinding: async (binding) => {
      bindings.push(binding)
      Object.assign(current, binding)
    },
  })
  const close = async () => {
    bridge.close()
    queue.close()
    await tg.close()
  }
  return { tg, api, queue, store, bridge, host, settings: current, bindings, close }
}

export const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))

/** Wait until the bridge's per-session output and the send queue are idle. */
export async function settle(env, sessionId) {
  for (let i = 0; i < 50; i += 1) {
    await sleep(20)
    const mirror = sessionId ? env.bridge.mirrors.get(sessionId) : undefined
    if (mirror) await mirror.queueTail
    if (env.queue.size === 0 && !env.queue.running) {
      await sleep(30)
      if (env.queue.size === 0 && !env.queue.running) return
    }
  }
}

let nextUpdate = 1
export function topicMessage(threadId, text, extra = {}) {
  return {
    message: {
      message_id: 5000 + nextUpdate++,
      from: { id: OWNER, is_bot: false, first_name: 'Owner' },
      chat: { id: CHAT, type: 'supergroup', is_forum: true },
      is_topic_message: true,
      message_thread_id: threadId,
      date: Math.floor(Date.now() / 1000),
      text,
      ...extra,
    },
  }
}

/** Minimal cordis-like ctx for wireEvents. */
export function fakeCtx(services = {}) {
  const listeners = new Map()
  return {
    listeners,
    on(name, fn) {
      if (!listeners.has(name)) listeners.set(name, [])
      listeners.get(name).push(fn)
      return () => {}
    },
    emit(name, ...args) {
      for (const fn of listeners.get(name) ?? []) fn(...args)
    },
    get: (name) => services[name],
  }
}

export function fakeSession(id, header = {}) {
  return { id, header: { cwd: '/home/u/dsh-tg-bridge', ...header } }
}

