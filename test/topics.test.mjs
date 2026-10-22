import assert from 'node:assert/strict'
import { test } from 'node:test'
import { Bridge } from '../lib/bridge.js'
import { wireEvents } from '../lib/host.js'
import { CHAT, fakeCtx, fakeSession, makeBridge, settle } from './harness.mjs'

test('first user message of a root session creates one topic named workspace · title', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  env.host.sessions.set('session-a', { cwd: '/home/u/codex-tg', workspaceTitle: 'codex-tg', title: 'Fix login' })
  env.bridge.onUserMessage('session-a', { text: 'hello', source: { kind: 'user' } })
  env.bridge.onTurnStarted('session-a')
  await settle(env, 'session-a')
  const created = env.tg.callsOf('createForumTopic')
  assert.equal(created.length, 1)
  assert.equal(created[0].params.name, 'codex-tg · Fix login')
  assert.equal(created[0].params.chat_id, CHAT)
  const record = env.store.topic('session-a')
  assert.equal(record.threadId, 101)
  assert.equal(env.store.sessionForThread(CHAT, 101), 'session-a')
  // header message is pinned as the status card
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'pinChatMessage'))
})

test('existing topic is reused (restart) and not recreated', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await env.store.putTopic('session-a', { chatId: CHAT, threadId: 555, name: 'x · y', closed: false })
  env.bridge.onUserMessage('session-a', { text: 'again', source: { kind: 'user' } })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
  const sent = env.tg.callsOf('sendMessage')
  assert.ok(sent.some((call) => call.params.message_thread_id === 555 && /again/.test(call.params.text)))
})

test('concurrent activity creates the topic only once', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  env.bridge.onSessionActive('session-b')
  env.bridge.onSessionActive('session-b')
  env.bridge.onTurnStarted('session-b')
  await settle(env, 'session-b')
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
})

test('title change renames the topic; archive closes it; restore reopens it', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  env.host.sessions.set('session-c', { cwd: '/w', workspaceTitle: 'api', title: '' })
  env.bridge.onSessionActive('session-c')
  await settle(env, 'session-c')
  assert.equal(env.tg.callsOf('createForumTopic')[0].params.name, 'api · New session')
  env.host.sessions.set('session-c', { cwd: '/w', workspaceTitle: 'api', title: 'Refactor auth' })
  env.bridge.onTitleChanged('session-c')
  await settle(env, 'session-c')
  const edit = env.tg.callsOf('editForumTopic')
  assert.equal(edit.length, 1)
  assert.equal(edit[0].params.name, 'api · Refactor auth')
  env.bridge.onSessionArchived('session-c')
  await settle(env, 'session-c')
  assert.equal(env.tg.callsOf('closeForumTopic').length, 1)
  assert.equal(env.store.topic('session-c').closed, true)
  env.bridge.onSessionRestored('session-c')
  await settle(env, 'session-c')
  assert.equal(env.tg.callsOf('reopenForumTopic').length, 1)
  assert.equal(env.store.topic('session-c').closed, false)
})

test('no topic is created before the group is bound', async (t) => {
  const env = await makeBridge({ bound: false })
  t.after(env.close)
  env.bridge.onUserMessage('session-a', { text: 'hello', source: { kind: 'user' } })
  await settle(env)
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
})

test('createTopicOn agent-created opens the topic as soon as the agent starts', async (t) => {
  const env = await makeBridge({ settings: { createTopicOn: 'agent-created' } })
  t.after(env.close)
  env.bridge.onSessionStarted('session-d')
  await settle(env, 'session-d')
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
})

test('subagent sessions never create topics; root sessions do (event wiring)', async () => {
  const calls = []
  const bridge = new Proxy({}, {
    get: (_target, prop) => (...args) => { calls.push([prop, ...args]) },
  })
  const root = fakeSession('session-root')
  const child = fakeSession('child-1', { origin: 'subagent', parentSession: 'session-root' })
  const ctx = fakeCtx({ sessions: { get: (id) => ({ 'session-root': root, 'child-1': child })[id] }, agents: { roots: () => [] } })
  wireEvents(ctx, () => bridge)
  ctx.emit('session/event', child, { type: 'user/message', data: { content: [{ type: 'text', text: 'task' }], source: { kind: 'user' } } })
  ctx.emit('session/event', child, { type: 'turn/start', data: { turn: 0 } })
  ctx.emit('session/event', child, { type: 'assistant/message', data: { message: { content: [{ type: 'text', text: 'child output' }] } } })
  ctx.emit('agent/created', { agent: { id: 'child-1', session: child }, source: 'startup' })
  assert.deepEqual(calls, [])
  ctx.emit('session/event', root, { type: 'user/message', data: { content: [{ type: 'text', text: 'hi' }], source: { kind: 'user', rpcId: 'gui-1' } } })
  assert.equal(calls[0][0], 'onUserMessage')
  assert.equal(calls[0][1], 'session-root')
})

test('agent/created announces only runtime roots', async () => {
  const calls = []
  const bridge = { onSessionStarted: (id) => calls.push(id) }
  const rootAgent = { id: 'session-r', session: fakeSession('session-r') }
  const forkChild = { id: 'fork-1', session: fakeSession('fork-1', { origin: 'subagent', parentSession: 'session-r', isSeeded: true }) }
  const ctx = fakeCtx({ agents: { roots: () => [rootAgent] } })
  wireEvents(ctx, () => bridge)
  ctx.emit('agent/created', { agent: forkChild, source: 'startup' })
  ctx.emit('agent/created', { agent: rootAgent, source: 'startup' })
  assert.deepEqual(calls, ['session-r'])
})

test('archive and restore are detected from workspace storage changes', async () => {
  const calls = []
  const bridge = { onSessionArchived: (id) => calls.push(['archived', id]), onSessionRestored: (id) => calls.push(['restored', id]) }
  const ctx = fakeCtx({ workspaceRegistry: { archivedSessionIds: ['old'] } })
  wireEvents(ctx, () => bridge)
  ctx.emit('domain/changed', { domain: 'workspace', table: '', key: '', operation: 'put', value: { archivedSessionIds: ['old', 'session-x'] } })
  ctx.emit('domain/changed', { domain: 'workspace', table: '', key: '', operation: 'put', value: { archivedSessionIds: ['session-x'] } })
  assert.deepEqual(calls, [['archived', 'session-x'], ['restored', 'old']])
})

test('Bridge is constructible without optional deps', () => {
  assert.ok(new Bridge({ api: {}, queue: {}, store: {}, host: {}, settings: () => ({}) }))
})
