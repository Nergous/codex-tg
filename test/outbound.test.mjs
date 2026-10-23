import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TELEGRAM_TEXT_LIMIT } from '../lib/format.js'
import { wireEvents } from '../lib/host.js'
import { CHAT, fakeCtx, fakeSession, makeBridge, settle, sleep } from './harness.mjs'

async function withTopic(env, sessionId = 'session-a') {
  await env.store.putTopic(sessionId, { chatId: CHAT, threadId: 300, name: 'ws · t', closed: false })
  return 300
}

const texts = (env) => env.tg.calls.filter((call) => call.method === 'sendMessage' || call.method === 'editMessageText').map((call) => `${call.method}:${call.params.text}`)

test('GUI user messages are mirrored, Telegram-originated ones are not echoed', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onUserMessage('session-a', { text: 'typed in DSH <b>', source: { kind: 'user', rpcId: 'gui-1' } })
  env.bridge.onUserMessage('session-a', { text: 'typed in Telegram', source: { kind: 'telegram' } })
  env.bridge.onUserMessage('session-a', { text: 'runtime context', source: { kind: 'runtime-context' } })
  await settle(env, 'session-a')
  const sent = env.tg.callsOf('sendMessage').map((call) => call.params.text)
  assert.equal(sent.length, 1)
  assert.match(sent[0], /👤/)
  assert.match(sent[0], /typed in DSH &lt;b&gt;/)
})

test('event wiring marks prompts sent from Telegram (tg- request ids) as telegram', () => {
  const seen = []
  const bridge = { onUserMessage: (id, msg) => seen.push(msg.source.kind) }
  const root = fakeSession('session-a')
  const ctx = fakeCtx({})
  wireEvents(ctx, () => bridge)
  ctx.emit('session/event', root, { type: 'user/message', data: { content: [{ type: 'text', text: 'x' }], source: { kind: 'user', rpcId: 'tg-123' } } })
  ctx.emit('session/event', root, { type: 'user/message', data: { content: [{ type: 'text', text: 'y' }], source: { kind: 'user', rpcId: 'abc' } } })
  assert.deepEqual(seen, ['telegram', 'user'])
})

test('streamed assistant text is sent once, edited while streaming, finalized without the cursor', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onTurnStarted('session-a')
  env.bridge.onAssistantDelta('session-a', 'att-1', 'Hello')
  await settle(env, 'session-a')
  await sleep(150)
  env.bridge.onAssistantDelta('session-a', 'att-1', 'Hello **world**')
  await sleep(250)
  await settle(env, 'session-a')
  env.bridge.onAssistantMessage('session-a', { attemptKey: 'att-1', text: 'Hello **world**, done.', toolCalls: [] })
  await settle(env, 'session-a')
  const sends = env.tg.callsOf('sendMessage').filter((call) => /Hello/.test(call.params.text))
  assert.equal(sends.length, 1, 'one message for the whole stream')
  assert.match(sends[0].params.text, /▍$/)
  assert.equal(sends[0].params.disable_notification, true)
  const edits = env.tg.callsOf('editMessageText')
  assert.ok(edits.length >= 1)
  assert.equal(edits.at(-1).params.text, 'Hello <b>world</b>, done.')
})

test('an abandoned attempt deletes its draft', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onAssistantDelta('session-a', 'att-1', 'partial')
  await settle(env, 'session-a')
  env.bridge.onAssistantAbandoned('session-a', 'att-1')
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('deleteMessage').length, 1)
})

test('tool calls collapse into one expandable block that updates with results', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onAssistantMessage('session-a', {
    text: 'Let me check.',
    toolCalls: [
      { id: 'c1', name: 'bash', input: { command: 'npm test' } },
      { id: 'c2', name: 'read', input: { file_path: 'src/a.js' } },
    ],
  })
  await settle(env, 'session-a')
  env.bridge.onToolResult('session-a', { callId: 'c1', isError: false })
  env.bridge.onToolResult('session-a', { callId: 'c2', isError: true, text: 'ENOENT: no such file' })
  await settle(env, 'session-a')
  const blocks = texts(env).filter((text) => text.includes('🛠'))
  assert.ok(blocks.length >= 2, 'block sent then edited')
  const last = blocks.at(-1)
  assert.match(last, /<blockquote expandable>/)
  assert.match(last, /2 tools · 1 ❌/)
  assert.match(last, /npm test/)
  assert.match(last, /ENOENT/)
  assert.ok(texts(env).some((text) => text.startsWith('sendMessage:Let me check.')))
})

test('text, tools, text keep their order as separate messages', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onAssistantMessage('session-a', { text: 'first', toolCalls: [{ id: 'c1', name: 'grep', input: { pattern: 'x' } }] })
  env.bridge.onToolResult('session-a', { callId: 'c1', isError: false })
  env.bridge.onAssistantMessage('session-a', { text: 'second', toolCalls: [] })
  await settle(env, 'session-a')
  const order = env.tg.callsOf('sendMessage').map((call) => (call.params.text.includes('🛠') ? 'tools' : call.params.text))
  assert.deepEqual(order, ['first', 'tools', 'second'])
})

test('a long answer becomes one preview message plus answer.md', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  const long = Array.from({ length: 400 }, (_, i) => `Line ${i}: ${'lorem ipsum '.repeat(3)}`).join('\n')
  env.bridge.onAssistantMessage('session-a', { text: long, toolCalls: [] })
  await settle(env, 'session-a')
  const sends = env.tg.callsOf('sendMessage')
  assert.equal(sends.length, 1)
  assert.ok(sends[0].params.text.length <= TELEGRAM_TEXT_LIMIT)
  assert.match(sends[0].params.text, /^Line 0:[\s\S]*Full answer in answer\.md/)
  const docs = env.tg.callsOf('sendDocument')
  assert.equal(docs.length, 1)
  assert.equal(docs[0].params.document.name, 'answer.md')
  assert.equal(docs[0].params.document.size, Buffer.byteLength(long.trim()))
})

test('a streamed long answer edits one message and ends with answer.md', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  const long = Array.from({ length: 400 }, (_, i) => `Line ${i}: ${'lorem ipsum '.repeat(3)}`).join('\n')
  env.bridge.onAssistantDelta('session-a', 'att-1', long.slice(0, 2000))
  await settle(env, 'session-a')
  env.bridge.onAssistantDelta('session-a', 'att-1', long)
  await sleep(150)
  await settle(env, 'session-a')
  env.bridge.onAssistantMessage('session-a', { attemptKey: 'att-1', text: long, toolCalls: [] })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('sendMessage').length, 1)
  assert.match(env.tg.callsOf('editMessageText').at(-1).params.text, /Full answer in answer\.md<\/i>$/)
  assert.equal(env.tg.callsOf('sendDocument').length, 1)
})

test('answerFileChars raises the file threshold: a long answer below it is sent as several messages', async (t) => {
  const env = await makeBridge({ settings: { answerFileChars: 100000 } })
  t.after(env.close)
  await withTopic(env)
  const long = Array.from({ length: 400 }, (_, i) => `Line ${i}: ${'lorem ipsum '.repeat(3)}`).join('\n')
  env.bridge.onAssistantDelta('session-a', 'att-1', long.slice(0, 2000))
  await settle(env, 'session-a')
  env.bridge.onAssistantMessage('session-a', { attemptKey: 'att-1', text: long, toolCalls: [] })
  await settle(env, 'session-a')
  const sends = env.tg.callsOf('sendMessage')
  assert.ok(sends.length > 1)
  assert.ok(sends.every((call) => call.params.text.length <= TELEGRAM_TEXT_LIMIT))
  assert.match(sends.at(-1).params.text, /Line 399:/)
  assert.equal(env.tg.callsOf('sendDocument').length, 0)
})

test('answerFileChars lowers the file threshold, also for final-only answers', async (t) => {
  const env = await makeBridge({ settings: { answerFileChars: 500, verbosity: 'final' } })
  t.after(env.close)
  await withTopic(env)
  env.bridge.onTurnStarted('session-a')
  env.bridge.onAssistantMessage('session-a', { text: 'word '.repeat(200), toolCalls: [] })
  env.bridge.onTurnEnded('session-a', { reason: 'completed' })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('sendDocument')[0].params.document.name, 'answer.md')
})

test('a short final-only answer stays a message', async (t) => {
  const env = await makeBridge({ settings: { verbosity: 'final' } })
  t.after(env.close)
  await withTopic(env)
  env.bridge.onTurnStarted('session-a')
  env.bridge.onAssistantMessage('session-a', { text: 'short **one**', toolCalls: [] })
  env.bridge.onTurnEnded('session-a', { reason: 'completed' })
  await settle(env, 'session-a')
  assert.ok(env.tg.callsOf('sendMessage').some((call) => call.params.text === 'short <b>one</b>'))
  assert.equal(env.tg.callsOf('sendDocument').length, 0)
})

test('subagent shows one line that is edited when it finishes', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onSubagentStarted('session-a', 'child-1', { description: 'Research API' })
  await settle(env, 'session-a')
  env.bridge.onSubagentEnded('session-a', 'child-1', { state: 'done' })
  await settle(env, 'session-a')
  const sends = env.tg.callsOf('sendMessage').filter((call) => /Subagent/.test(call.params.text))
  assert.equal(sends.length, 1)
  assert.match(sends[0].params.text, /Research API.*waiting for result ⏳/)
  const edit = env.tg.callsOf('editMessageText').filter((call) => /Subagent/.test(call.params.text)).at(-1)
  assert.match(edit.params.text, /done ✅/)
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
})

test('subagent events map to the root topic and wait for the catalog label', async () => {
  const calls = []
  const bridge = {
    onSubagentStarted: (...args) => calls.push(['start', ...args]),
    onSubagentEnded: (...args) => calls.push(['end', ...args]),
    onSubagentLabel: () => {},
  }
  const root = fakeSession('session-root')
  const child = fakeSession('child-1', { origin: 'subagent', parentSession: 'session-root' })
  const ctx = fakeCtx({ sessions: { get: (id) => ({ 'session-root': root, 'child-1': child })[id] } })
  wireEvents(ctx, () => bridge)
  ctx.emit('subagent/start', { id: 'child-1', provider: 'spawn', runId: 'r1' })
  ctx.emit('session/event', root, { type: 'subagent/catalog', data: { childId: 'child-1', label: 'Scan repo', mode: 'one-shot' } })
  await sleep(500)
  ctx.emit('subagent/end', { id: 'child-1', stopReason: 'completed' })
  await sleep(550)
  assert.deepEqual(calls[0].slice(0, 3), ['start', 'session-root', 'child-1'])
  assert.equal(calls[0][3].description, 'Scan repo')
  assert.deepEqual(calls[1].slice(0, 3), ['end', 'session-root', 'child-1'])
  assert.equal(calls[1][3].state, 'done')
})

test('turn end posts a notifying Done line; stop and failure are reported', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onTurnStarted('session-a')
  env.bridge.onTurnEnded('session-a', { reason: 'completed', usage: { contextTokens: 45000 } })
  env.bridge.onTurnStarted('session-a')
  env.bridge.onTurnEnded('session-a', { reason: 'cancelled' })
  env.bridge.onTurnStarted('session-a')
  env.bridge.onTurnEnded('session-a', { reason: 'error', error: 'rate limited' })
  await settle(env, 'session-a')
  const lines = env.tg.callsOf('sendMessage').map((call) => call.params)
  assert.match(lines[0].text, /✅ Done · \d+s · ctx 45\.0k/)
  assert.equal(lines[0].disable_notification, false)
  assert.match(lines[1].text, /⏹ Stopped/)
  assert.match(lines[2].text, /⚠️ Turn failed: rate limited/)
})

test('verbosity final sends only the last answer at turn end', async (t) => {
  const env = await makeBridge({ settings: { verbosity: 'final' } })
  t.after(env.close)
  await withTopic(env)
  env.bridge.onTurnStarted('session-a')
  env.bridge.onAssistantDelta('session-a', 'a1', 'thinking out loud')
  env.bridge.onAssistantMessage('session-a', { attemptKey: 'a1', text: 'intermediate', toolCalls: [{ id: 'c', name: 'bash', input: { command: 'ls' } }] })
  env.bridge.onAssistantMessage('session-a', { attemptKey: 'a2', text: 'Final answer', toolCalls: [] })
  env.bridge.onTurnEnded('session-a', { reason: 'completed' })
  await settle(env, 'session-a')
  const sent = env.tg.callsOf('sendMessage').map((call) => call.params.text)
  assert.deepEqual(sent.filter((text) => !text.startsWith('✅')), ['Final answer'])
})

test('muted topic receives no agent output', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  await env.store.patchTopic('session-a', { muted: true })
  env.bridge.onUserMessage('session-a', { text: 'x', source: { kind: 'user' } })
  env.bridge.onAssistantMessage('session-a', { text: 'answer', toolCalls: [{ id: 'c', name: 'bash', input: {} }] })
  env.bridge.onTurnEnded('session-a', { reason: 'completed' })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('sendMessage').length, 0)
})

test('presented files are sent to the topic as documents', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onFilesPresented('session-a', [{ path: 'out/report.txt', description: 'Report' }])
  await settle(env, 'session-a')
  const docs = env.tg.callsOf('sendDocument')
  assert.equal(docs.length, 1)
  assert.equal(docs[0].params.document.name, 'report.txt')
  assert.equal(docs[0].params.caption, 'Report')
})
