/**
 * Persistent bridge state in a DSH storage domain (`~/.dsh/storages`).
 *
 * Tables:
 * - `topics`: session id -> the forum topic mirroring it.
 * - `state`: singleton records (`poller` offset, `chat` binding).
 *
 * The bridge only touches the store through {@link BridgeStore}; tests use
 * {@link memoryDomain} with the same table interface.
 */

const isRecord = (value) => value !== null && typeof value === 'object' && !Array.isArray(value)

function schema(parse) {
  return {
    parse,
    safeParse(value) {
      try {
        return { success: true, data: parse(value) }
      } catch (error) {
        return { success: false, error }
      }
    },
  }
}

export function parseTopic(value) {
  if (!isRecord(value)) throw new TypeError('topic record must be an object')
  const { chatId, threadId } = value
  if (!Number.isSafeInteger(chatId) || !Number.isSafeInteger(threadId)) throw new TypeError('topic record needs integer chatId and threadId')
  const out = { chatId, threadId }
  if (typeof value.name === 'string') out.name = value.name
  if (typeof value.closed === 'boolean') out.closed = value.closed
  if (typeof value.cwd === 'string') out.cwd = value.cwd
  if (typeof value.verbosity === 'string') out.verbosity = value.verbosity
  if (typeof value.muted === 'boolean') out.muted = value.muted
  if (Number.isSafeInteger(value.statusMessageId)) out.statusMessageId = value.statusMessageId
  if (Number.isSafeInteger(value.createdAt)) out.createdAt = value.createdAt
  return out
}

export function parseState(value) {
  if (!isRecord(value)) throw new TypeError('state record must be an object')
  return JSON.parse(JSON.stringify(value))
}

export const DOMAIN = {
  name: 'telegram_bridge',
  version: 1,
  invalidRecords: 'backup-and-skip',
  tables: {
    topics: { valueSchema: schema(parseTopic) },
    state: { valueSchema: schema(parseState) },
  },
}

/** In-memory domain with the storage-domain table interface (tests, and before storage opens). */
export function memoryDomain() {
  const tables = new Map()
  const table = (name) => {
    if (!tables.has(name)) tables.set(name, new Map())
    const map = tables.get(name)
    return {
      get: (key) => map.get(key),
      put: async (key, value) => { map.set(key, value) },
      delete: async (key) => { map.delete(key) },
      entries: () => map.entries(),
    }
  }
  return { table, close() {} }
}

export class BridgeStore {
  constructor(domain = memoryDomain()) {
    this.domain = domain
    this.writes = Promise.resolve()
  }

  serialize(task) {
    const run = this.writes.then(task)
    this.writes = run.catch(() => undefined)
    return run
  }

  topic(sessionId) {
    return this.domain.table('topics').get(sessionId)
  }

  topics() {
    return [...this.domain.table('topics').entries()]
  }

  /** Session id of the topic `threadId` in `chatId`, if mirrored. */
  sessionForThread(chatId, threadId) {
    for (const [sessionId, record] of this.domain.table('topics').entries()) {
      if (record.chatId === chatId && record.threadId === threadId) return sessionId
    }
    return undefined
  }

  putTopic(sessionId, record) {
    return this.serialize(() => this.domain.table('topics').put(sessionId, parseTopic(record)))
  }

  patchTopic(sessionId, patch) {
    return this.serialize(async () => {
      const current = this.domain.table('topics').get(sessionId)
      if (!current) return undefined
      const next = parseTopic({ ...current, ...patch })
      await this.domain.table('topics').put(sessionId, next)
      return next
    })
  }

  deleteTopic(sessionId) {
    return this.serialize(() => this.domain.table('topics').delete(sessionId))
  }

  state(key) {
    return this.domain.table('state').get(key)
  }

  putState(key, value) {
    return this.serialize(() => this.domain.table('state').put(key, parseState(value)))
  }
}
