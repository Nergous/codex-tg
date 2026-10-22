/**
 * DSH side of the bridge: the `host` adapter the Bridge calls, and the cordis
 * event wiring that feeds DSH activity into the Bridge.
 *
 * Every session operation goes through `ctx.sessionController`, the same
 * service the DSH web GUI uses, so Telegram input looks exactly like input
 * typed in the GUI (source `{ kind: 'user', rpcId }`).
 */
import { randomUUID } from 'node:crypto'
import { basename } from 'node:path'
import { gitDiff, readPresentedFile, runTranscribeCommand, toPcm16k, wavFromPcm } from './host-utils.js'

/** Request ids of prompts sent from Telegram start with this prefix (echo suppression). */
export const TG_REQUEST_PREFIX = 'tg-'

const PRESENT_TOOLS = new Set(['present'])
const MAX_TRACKED_CALLS = 500

const textOf = (content) => (Array.isArray(content) ? content : [])
  .filter((block) => block?.type === 'text' && typeof block.text === 'string')
  .map((block) => block.text)
  .join('\n')

const parseJson = (raw) => {
  if (typeof raw !== 'string') return raw
  try {
    return JSON.parse(raw)
  } catch {
    return raw
  }
}

/** Is this session a user-visible root (not a subagent / fork child)? */
export function isRootSession(session) {
  return session?.header?.origin !== 'subagent'
}

/**
 * @param {object} ctx - plugin context (cordis).
 * @param {() => object} settings - live effective config.
 */
export function createHost(ctx, settings) {
  const svc = (name) => {
    try {
      return ctx.get(name)
    } catch {
      return undefined
    }
  }
  const controller = () => {
    const sc = svc('sessionController')
    if (!sc) throw new Error('DSH session controller is not available')
    return sc
  }
  /** sessionId -> { cwd, title, updatedAt } learned from listings (cold sessions). */
  const known = new Map()

  async function resolveAgent(sessionId) {
    const live = svc('agents')?.get(sessionId)
    if (live) return live
    const result = await controller().resolveAgent(sessionId)
    if (result?.error) throw new Error(result.error.message ?? String(result.error.code ?? 'cannot resume session'))
    return result?.agent ?? result
  }

  function workspaceFor(sessionId, cwd) {
    const list = svc('workspaceRegistry')?.list?.() ?? []
    return list.find((ws) => ws.sessionIds?.includes?.(sessionId)) ?? (cwd ? list.find((ws) => ws.path === cwd) : undefined)
  }

  function snapshot(session, keys) {
    try {
      return svc('sessionProjections')?.snapshot?.(session, keys)?.values ?? {}
    } catch {
      return {}
    }
  }

  function modelOf(session) {
    if (!session) return undefined
    const selection = snapshot(session, ['modelSelection']).modelSelection
    const pick = selection?.next ?? selection?.lastUsed
    if (pick?.model) return { provider: pick.provider, model: pick.model }
    try {
      const config = session.requestHeader?.()?.config
      if (config?.model) return { provider: config.provider, model: config.model }
    } catch {
      // older builds
    }
    return undefined
  }

  const host = {
    async sessionInfo(sessionId) {
      const session = svc('sessions')?.get(sessionId)
      const cached = known.get(sessionId) ?? {}
      const cwd = session?.header?.cwd ?? cached.cwd
      let title
      if (session) {
        try {
          title = svc('sessionTitle')?.get?.(session)?.title
        } catch {
          title = undefined
        }
      }
      title ??= cached.title
      const workspace = workspaceFor(sessionId, cwd)
      const model = modelOf(session)
      return {
        sessionId,
        cwd,
        title: title ?? undefined,
        workspaceTitle: workspace?.title ?? (cwd ? basename(cwd) : undefined),
        workspaceId: workspace?.id,
        model: model ? model.model : undefined,
        modelId: model ? `${model.provider}/${model.model}` : undefined,
      }
    },

    async sessionStats(sessionId) {
      const session = svc('sessions')?.get(sessionId)
      if (!session) return {}
      const values = snapshot(session, ['contextPressure', 'tokenUsage'])
      const pressure = values.contextPressure ?? {}
      return {
        contextTokens: pressure.projectedTokens ?? pressure.pressureTokens,
        contextWindow: pressure.contextWindow,
        outputTokens: values.tokenUsage?.outputTokens,
      }
    },

    isRunning(sessionId) {
      return svc('agents')?.get(sessionId)?.status === 'running'
    },

    /**
     * Send Telegram input to the session through the GUI's prompt path.
     * @param {{ text: string, attachments: Array<{ kind: 'image' | 'file', data: Buffer, mime: string, name: string }> }} input
     */
    async sendInput(sessionId, input, { mode = 'followup' } = {}) {
      const content = []
      if (input.text) content.push({ type: 'text', text: input.text })
      const notes = []
      for (const attachment of input.attachments ?? []) {
        if (attachment.kind === 'image' && /^image\/(png|jpeg|webp|gif)$/.test(attachment.mime)) {
          content.push({ type: 'image', mediaType: attachment.mime, data: attachment.data.toString('base64'), name: attachment.name })
          continue
        }
        const uploads = svc('fileUploads')
        if (uploads) {
          const agent = await resolveAgent(sessionId)
          const { receiptId } = await uploads.upload(agent, { data: attachment.data.toString('base64'), name: attachment.name }, AbortSignal.timeout(60000))
          content.push({ type: 'file', receiptId })
        } else {
          notes.push(`(attachment ${attachment.name} could not be delivered: file uploads unavailable)`)
        }
      }
      if (notes.length) content.push({ type: 'text', text: notes.join('\n') })
      if (content.length === 0) throw new Error('nothing to send')
      const requestId = `${TG_REQUEST_PREFIX}${randomUUID()}`
      await controller().prompt({
        sessionId,
        requestId,
        mode: mode === 'steer' ? 'steer' : 'queue',
        content,
        clientTimeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      }, AbortSignal.timeout(120000))
      return requestId
    },

    async cancel(sessionId) {
      const agent = svc('agents')?.get(sessionId)
      if (!agent || agent.status !== 'running') return false
      await controller().cancel({ sessionId })
      return true
    },

    async compact(sessionId) {
      const agent = await resolveAgent(sessionId)
      if (agent.status === 'running') throw new Error('the session is busy; /stop it or wait until the turn ends')
      const commands = svc('commands')
      if (commands?.execute) {
        const outcome = await commands.execute(agent, '/compact', [], AbortSignal.timeout(10 * 60000))
        const result = outcome?.result
        if (result?.kind === 'error') throw new Error(result.text ?? 'compaction failed')
        return result?.text
      }
      const compaction = svc('compaction')
      if (!compaction?.compactNow) throw new Error('compaction is not available')
      const done = await compaction.compactNow(agent, AbortSignal.timeout(10 * 60000))
      return done ? `Compacted ${done.shadowedTokenCount ?? ''} tokens.`.replace('  ', ' ') : 'Nothing to compact.'
    },

    /**
     * Run a DSH slash command (/goal, /permission, plugin commands) like the
     * composer does. Returns undefined when DSH does not know the command.
     */
    async runCommand(sessionId, line) {
      const commands = svc('commands')
      if (!commands?.execute) return undefined
      const agent = await resolveAgent(sessionId)
      const outcome = await commands.execute(agent, line, [], AbortSignal.timeout(10 * 60000))
      if (!outcome) return undefined
      return { ok: outcome.result?.kind !== 'error', text: outcome.result?.text }
    },

    async diff(sessionId) {
      const info = await host.sessionInfo(sessionId)
      return gitDiff(info.cwd)
    },

    async listModels() {
      const catalog = await controller().modelCatalog?.()
      const out = []
      for (const group of catalog?.groups ?? []) {
        for (const model of group.models ?? []) out.push({ id: `${group.id}/${model.id}`, provider: group.id, model: model.id, label: model.name ?? model.id })
      }
      return out
    },

    async setModel(sessionId, target) {
      const models = await host.listModels()
      const wanted = String(target).trim().toLowerCase()
      const model = models.find((entry) => entry.id.toLowerCase() === wanted)
        ?? models.find((entry) => entry.model.toLowerCase() === wanted)
        ?? models.find((entry) => entry.label.toLowerCase() === wanted)
        ?? models.find((entry) => entry.label.toLowerCase().includes(wanted) || entry.model.toLowerCase().includes(wanted))
      if (!model) throw new Error(`unknown model "${target}"`)
      await resolveAgent(sessionId)
      await controller().selectModel({ sessionId, provider: model.provider, model: model.model })
      return model.label
    },

    async listSessions({ limit = 15 } = {}) {
      const registry = svc('workspaceRegistry')
      const archived = new Set(registry?.archivedSessionIds ?? [])
      const { items = [] } = (await controller().list({})) ?? {}
      const out = []
      for (const item of items) {
        if (item.origin === 'subagent' || archived.has(item.sessionId) || item.blank) continue
        const title = item.projections?.values?.title ?? undefined
        known.set(item.sessionId, { cwd: item.cwd, title })
        const workspace = workspaceFor(item.sessionId, item.cwd)
        out.push({ sessionId: item.sessionId, title, cwd: item.cwd, running: item.running === true, updatedAt: item.updatedAt, workspaceTitle: workspace?.title ?? (item.cwd ? basename(item.cwd) : '') })
        if (out.length >= limit) break
      }
      return out
    },

    async listWorkspaces() {
      return (svc('workspaceRegistry')?.list?.() ?? []).map((ws) => ({ id: ws.id, title: ws.title, path: ws.path }))
    },

    async newSession(path, first) {
      const workspace = (svc('workspaceRegistry')?.list?.() ?? []).find((ws) => ws.path === path)
      const created = await controller().create(workspace ? { workspaceId: workspace.id } : { cwd: path })
      if (first?.text) await host.sendInput(created.sessionId, { text: first.text, attachments: [] })
      return created.sessionId
    },

    async readFile(sessionId, path) {
      const info = await host.sessionInfo(sessionId)
      return readPresentedFile(path, info.cwd)
    },

    /**
     * Voice note to text: DSH speech-to-text when loaded, else the
     * configured command. Returns undefined when neither is available.
     */
    async transcribe(data) {
      const cfg = settings()
      const stt = svc('speechToText')
      const command = String(cfg.transcribeCommand ?? '').trim()
      if (!stt && !command) return undefined
      const wav = wavFromPcm(await toPcm16k(data, { ffmpeg: cfg.ffmpegPath || 'ffmpeg' }))
      if (command) return runTranscribeCommand(command, wav)
      const spec = stt.resolve({ audio: new Uint8Array(wav), ...cfg.transcribeLanguage ? { language: cfg.transcribeLanguage } : {} })
      const result = await stt.transcribe(spec, AbortSignal.timeout(180000))
      return String(result?.text ?? '').trim() || undefined
    },
  }
  return host
}

/**
 * Subscribe to DSH events and feed them to the bridge.
 * @param {object} ctx - plugin context.
 * @param {() => import('./bridge.js').Bridge | undefined} getBridge
 */
export function wireEvents(ctx, getBridge) {
  /** sessionId -> { attemptId, text } for the attempt being streamed */
  const attempts = new Map()
  /** sessionId -> Map(callId -> { name, args }) */
  const calls = new Map()
  /** child sessionId -> { root, label } */
  const children = new Map()
  /** childId -> label from the parent's subagent/catalog */
  const labels = new Map()
  let archived = new Set(ctx.get('workspaceRegistry')?.archivedSessionIds ?? [])

  const bridge = () => getBridge()
  const sessions = () => ctx.get('sessions')

  function rootOf(sessionId, depth = 0) {
    const session = sessions()?.get(sessionId)
    if (!session) return children.get(sessionId)?.root
    if (isRootSession(session)) return sessionId
    const parent = session.header?.parentSession
    if (!parent || depth > 10) return undefined
    return rootOf(parent, depth + 1)
  }

  function rememberCall(sessionId, callId, name, args) {
    if (!callId) return
    let map = calls.get(sessionId)
    if (!map) calls.set(sessionId, (map = new Map()))
    map.set(callId, { name, args: compactArgs(args) })
    if (map.size > MAX_TRACKED_CALLS) map.delete(map.keys().next().value)
  }

  function onSessionEvent(session, event) {
    const b = bridge()
    if (!b || !session || !event) return
    const sessionId = session.id
    const data = event.data ?? {}
    if (!isRootSession(session)) {
      // Remember subagent tool calls too: their approvals land in the root topic.
      if (event.type === 'tool/call') rememberCall(sessionId, data.callId, data.name, parseJson(data.arguments))
      return
    }
    switch (event.type) {
      case 'user/message': {
        const source = data.source ?? {}
        const kind = source.kind === 'user' && String(source.rpcId ?? '').startsWith(TG_REQUEST_PREFIX) ? 'telegram' : source.kind
        const images = (data.content ?? []).filter((block) => block?.type === 'image').length
        b.onUserMessage(sessionId, { text: textOf(data.content), source: { ...source, kind }, images })
        break
      }
      case 'turn/start':
        b.onTurnStarted(sessionId)
        break
      case 'turn/end': {
        const reason = data.reason ?? {}
        const kind = reason.kind === 'aborted' ? 'cancelled' : reason.kind === 'max-tokens' ? 'error' : reason.kind
        const error = reason.kind === 'max-tokens' ? 'output token limit reached' : reason.error?.message ?? reason.error?.reason ?? (reason.error ? String(reason.error.kind ?? '') : undefined)
        let usage
        try {
          const pressure = ctx.get('sessionProjections')?.snapshot?.(session, ['contextPressure'])?.values?.contextPressure
          usage = { contextTokens: pressure?.projectedTokens ?? pressure?.pressureTokens }
        } catch {
          usage = undefined
        }
        attempts.delete(sessionId)
        calls.delete(sessionId)
        b.onTurnEnded(sessionId, { reason: kind, error, usage })
        break
      }
      case 'assistant/message': {
        const message = data.message ?? {}
        const content = message.content ?? []
        const toolCalls = content
          .filter((block) => block?.type === 'tool-call')
          .map((block) => ({ id: block.id, name: block.name, input: parseJson(block.arguments) }))
        for (const call of toolCalls) rememberCall(sessionId, call.id, call.name, call.input)
        const attempt = attempts.get(sessionId)
        b.onAssistantMessage(sessionId, { attemptKey: attempt?.attemptId, text: textOf(content), toolCalls })
        attempts.delete(sessionId)
        break
      }
      case 'tool/call':
        rememberCall(sessionId, data.callId, data.name, parseJson(data.arguments))
        break
      case 'tool/result': {
        const message = data.message ?? {}
        const callId = message.toolCallId ?? message.source?.callId
        const isError = message.isError === true
        b.onToolResult(sessionId, { callId, isError, text: isError ? textOf(message.content) || data.error?.reason : undefined })
        const call = calls.get(sessionId)?.get(callId)
        if (!isError && call && PRESENT_TOOLS.has(call.name)) {
          const files = Array.isArray(call.args?.files) ? call.args.files.filter((file) => typeof file?.path === 'string') : []
          if (files.length) b.onFilesPresented(sessionId, files)
        }
        break
      }
      case 'session/title':
        b.onTitleChanged(sessionId, data.title)
        break
      case 'subagent/catalog':
        if (data.childId && data.label) {
          labels.set(data.childId, data.label)
          b.onSubagentLabel(sessionId, data.childId, data.label)
        }
        break
      default:
        break
    }
  }

  ctx.on('session/event', onSessionEvent)

  ctx.on('agent/created', ({ agent } = {}) => {
    const b = bridge()
    if (!b || !agent?.session) return undefined
    const roots = ctx.get('agents')?.roots?.() ?? []
    if (isRootSession(agent.session) && roots.includes(agent)) b.onSessionStarted(agent.id)
    return undefined
  })

  ctx.on('agent/assistant-stream', ({ agent, frame } = {}) => {
    const b = bridge()
    if (!b || !agent?.session || !frame || !isRootSession(agent.session)) return
    const sessionId = agent.id
    if (frame.type === 'start') {
      attempts.set(sessionId, { attemptId: frame.attemptId, text: '' })
      return
    }
    const attempt = attempts.get(sessionId)
    if (!attempt || attempt.attemptId !== frame.attemptId) return
    if (frame.type === 'chunk' && frame.chunk?.type === 'text-delta') {
      attempt.text += frame.chunk.text ?? ''
      b.onAssistantDelta(sessionId, attempt.attemptId, attempt.text)
      return
    }
    if (frame.type === 'end' && (frame.outcome?.kind === 'abandoned' || frame.outcome?.eventType === 'assistant/attempt')) {
      b.onAssistantAbandoned(sessionId, attempt.attemptId)
      attempts.delete(sessionId)
    }
  })

  ctx.on('subagent/start', (info = {}) => {
    const b = bridge()
    if (!b || !info.id) return
    const child = sessions()?.get(info.id)
    const parent = child?.header?.parentSession
    const root = parent ? rootOf(parent) : undefined
    if (!root) return
    children.set(info.id, { root })
    // The catalog label is appended to the parent around the same time; give it a moment.
    setTimeout(() => {
      const current = bridge()
      if (!current) return
      let label = labels.get(info.id)
      if (!label && child) {
        try {
          const first = child.eventAt?.(0)
          if (first?.type === 'subagent/descriptor') label = first.data?.label
        } catch {
          // descriptor unavailable
        }
      }
      current.onSubagentStarted(root, info.id, { description: label, provider: info.provider })
    }, 400)
  })

  ctx.on('subagent/end', (info = {}) => {
    const b = bridge()
    const entry = children.get(info?.id)
    if (!b || !entry) return
    const state = info.stopReason === 'completed' ? 'done' : info.stopReason === 'aborted' ? 'cancelled' : 'failed'
    setTimeout(() => {
      const description = labels.get(info.id)
      children.delete(info.id)
      labels.delete(info.id)
      calls.delete(info.id)
      bridge()?.onSubagentEnded(entry.root, info.id, { state, description })
    }, 450)
  })

  ctx.on('domain/changed', (change = {}) => {
    const b = bridge()
    if (!b || change.domain !== 'workspace' || change.table !== '' || change.operation !== 'put') return
    const next = new Set(change.value?.archivedSessionIds ?? [])
    for (const id of next) if (!archived.has(id)) b.onSessionArchived(id)
    for (const id of archived) if (!next.has(id)) b.onSessionRestored(id)
    archived = next
  })

  return {
    /** Arguments of a recorded tool call (approval previews). */
    callArgs(sessionId, callId) {
      return calls.get(sessionId)?.get(callId)
    },
    rootOf,
  }
}

const PREVIEW_ARG_KEYS = ['command', 'file_path', 'path', 'url']

/**
 * What is kept of a tool call's arguments: the preview fields and presented
 * file paths, never bulky content (file bodies, patches).
 */
export function compactArgs(args) {
  if (!args || typeof args !== 'object') return typeof args === 'string' ? args.slice(0, 500) : undefined
  const out = {}
  for (const key of PREVIEW_ARG_KEYS) if (typeof args[key] === 'string') out[key] = args[key].slice(0, 2000)
  if (Array.isArray(args.files)) {
    out.files = args.files.slice(0, 20).filter((file) => typeof file?.path === 'string').map((file) => ({ path: file.path, ...(typeof file.description === 'string' ? { description: file.description.slice(0, 1000) } : {}) }))
  }
  if (Object.keys(out).length > 0) return out
  const text = JSON.stringify(args)
  return text === '{}' ? undefined : text.slice(0, 500)
}

/** One-line preview of what an approval is about, like the GUI shows. */
export function approvalPreview(call) {
  if (!call) return undefined
  const args = call.args
  if (args && typeof args === 'object') {
    if (typeof args.command === 'string') return args.command
    if (typeof args.file_path === 'string') return args.file_path
    if (typeof args.path === 'string') return args.path
    if (typeof args.url === 'string') return args.url
  }
  const text = typeof args === 'string' ? args : JSON.stringify(args)
  return text && text !== '{}' ? text : undefined
}
