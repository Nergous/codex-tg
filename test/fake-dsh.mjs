/**
 * Fake DSH host context for plugin-level tests: a cordis-like ctx with event
 * dispatch (emit + waterfall honouring `prepend`), effects, inject, and the
 * DSH services the plugin uses. Never touches a real DSH installation.
 */
import { Readable } from 'node:stream'
import { memoryDomain } from '../lib/store.js'

export function createFakeDsh({ token, workspaces = [{ id: 'ws1', title: 'codex-tg', path: '/home/u/codex-tg', sessionIds: ['session-a'] }] } = {}) {
  const hooks = new Map()
  const disposers = []
  const routes = new Map()
  const prompts = []
  const sessions = new Map()
  const agents = new Map()
  const credentialStore = new Map(token ? [['DSH_TELEGRAM_BOT_TOKEN', token]] : [])
  const settingsWrites = []
  const logs = []

  const addSession = (id, header = {}) => {
    const session = { id, header: { cwd: '/home/u/codex-tg', ...header }, eventAt: () => undefined }
    sessions.set(id, session)
    const agent = { id, session, status: 'idle', cancelled: [] }
    agent.cancel = (cause, options) => agent.cancelled.push([cause, options])
    agents.set(id, agent)
    return { session, agent }
  }

  const services = {
    storageDomain: { open: async () => memoryDomain() },
    sessions: { get: (id) => sessions.get(id) },
    agents: {
      get: (id) => agents.get(id),
      roots: () => [...agents.values()].filter((agent) => agent.session.header.origin !== 'subagent'),
    },
    sessionController: {
      resolveAgent: async (id) => (agents.has(id) ? { agent: agents.get(id) } : { error: { message: `session "${id}" not found` } }),
      prompt: async (request, signal) => {
        signal.throwIfAborted()
        prompts.push(request)
        return { accepted: true }
      },
      cancel: async ({ sessionId }) => {
        agents.get(sessionId)?.cancel({ kind: 'user' }, { keepInbox: true })
        return {}
      },
      list: async () => ({ items: [...sessions.values()].map((s) => ({ sessionId: s.id, cwd: s.header.cwd, origin: s.header.origin, updatedAt: 1, running: false, projections: { values: { title: `Title of ${s.id}` } } })) }),
      create: async (request) => {
        const id = `session-new-${sessions.size}`
        addSession(id)
        return { sessionId: id, request }
      },
      modelCatalog: async () => ({ groups: [{ id: 'p', name: 'P', models: [{ id: 'm1', name: 'Model One' }] }] }),
      selectModel: async (request) => ({ selected: request }),
    },
    sessionTitle: { get: (session) => ({ title: `Title of ${session.id}` }) },
    workspaceRegistry: { list: () => workspaces, archivedSessionIds: [] },
    credentials: {
      resolve: async (ref) => (credentialStore.has(ref) ? { value: credentialStore.get(ref), source: 'file' } : undefined),
      describe: async (ref) => ({ configured: credentialStore.has(ref), source: 'file', writable: true }),
      set: async (ref, value) => { credentialStore.set(ref, value) },
      unset: async (ref) => { credentialStore.delete(ref) },
    },
    settings: {
      describe: () => [{ ns: 'telegram-bridge', revision: settingsWrites.length }],
      mutate: async (ns, ops, revision) => {
        settingsWrites.push({ ns, ops, revision })
        return true
      },
      configure: () => () => {},
    },
    webServer: {
      register: (route) => {
        routes.set(route.path, route.handler)
        return () => routes.delete(route.path)
      },
    },
    connection: { requestRejection: () => undefined },
  }

  const ctx = {
    fiber: {},
    logger: {
      info: (...args) => logs.push(['info', ...args]),
      warn: (...args) => logs.push(['warn', ...args]),
      error: (...args) => logs.push(['error', ...args]),
      debug: () => {},
    },
    get: (name) => services[name],
    on(name, fn, options) {
      const prepend = typeof options === 'object' ? options?.prepend : options
      if (!hooks.has(name)) hooks.set(name, [])
      hooks.get(name)[prepend ? 'unshift' : 'push'](fn)
      return () => {
        const list = hooks.get(name)
        const index = list.indexOf(fn)
        if (index >= 0) list.splice(index, 1)
      }
    },
    effect(execute) {
      const dispose = execute()
      if (typeof dispose === 'function') disposers.push(dispose)
      return () => {}
    },
    inject(names, callback) {
      if (names.every((n) => services[n] !== undefined)) callback(ctx)
    },
    provide() {},
    ...services,
  }

  return {
    ctx,
    services,
    routes,
    prompts,
    sessions,
    agents,
    credentialStore,
    settingsWrites,
    logs,
    addSession,
    emit(name, ...args) {
      for (const fn of [...(hooks.get(name) ?? [])]) fn.call(this, ...args)
    },
    /** cordis-style waterfall: listeners outermost-first, `inner` last. */
    waterfall(name, request, inner) {
      const chain = [...(hooks.get(name) ?? [])]
      const next = () => (chain.shift() ?? inner)(request, next)
      return next()
    },
    async dispose() {
      for (const dispose of disposers.reverse()) await dispose()
    },
    /** Call one registered HTTP route. */
    async request(path, { method = 'GET', body } = {}) {
      const handler = routes.get(path)
      if (!handler) throw new Error(`no route ${path}`)
      const req = Object.assign(Readable.from(body === undefined ? [] : [Buffer.from(JSON.stringify(body))]), { method, url: path, headers: {} })
      let payload
      const res = {
        statusCode: 0,
        headersSent: false,
        setHeader() {},
        end(text) {
          payload = text ? JSON.parse(text) : undefined
        },
      }
      await handler(req, res)
      return { status: res.statusCode, body: payload }
    },
  }
}
