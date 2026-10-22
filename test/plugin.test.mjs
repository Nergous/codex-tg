import assert from 'node:assert/strict'
import { existsSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { test } from 'node:test'
import { DEFAULTS, ROUTES, buildConfig, resolveConfig } from '../index.js'
import { CHAT, OWNER, TOKEN, sleep, topicMessage } from './harness.mjs'
import { bootPlugin } from './plugin-env.mjs'

test('resolveConfig fills defaults, unwraps volatile refs, and sanitizes', () => {
  const ref = (value) => ({ get: () => value })
  const cfg = resolveConfig({ chatId: ref(String(CHAT)), allowedUserIds: ref(['5', 'x', 6]), verbosity: 'bogus', tokenRef: 'bad ref!', unknown: 1 })
  assert.equal(cfg.chatId, CHAT)
  assert.deepEqual(cfg.allowedUserIds, [5, 6])
  assert.equal(cfg.verbosity, 'tools')
  assert.equal(cfg.tokenRef, 'DSH_TELEGRAM_BOT_TOKEN')
  assert.equal('unknown' in cfg, false)
  assert.equal(cfg.streaming, DEFAULTS.streaming)
})

/** Real schemastery from an extracted DSH runtime, when available (DSH_NODE_MODULES or the local scratch copy). */
async function loadRealSchemastery() {
  const roots = [process.env.DSH_NODE_MODULES, join(dirname(new URL(import.meta.url).pathname), '..', '.scratch', 'x', 'dsh', 'node_modules')].filter(Boolean)
  for (const root of roots) {
    const entry = join(root, '@deepseek-ai', 'schemastery', 'lib', 'index.mjs')
    if (existsSync(entry)) {
      const mod = await import(pathToFileURL(entry).href)
      return mod.default ?? mod.Schema
    }
  }
  return undefined
}

test('Config schema validates with the real schemastery, every field volatile', async (t) => {
  const z = await loadRealSchemastery()
  if (!z) return t.skip('DSH schemastery not available')
  const schema = buildConfig(z)
  const value = schema({ chatId: CHAT, verbosity: 'final' })
  const plain = (v) => (v && typeof v === 'object' && typeof v.get === 'function' ? v.get() : v)
  assert.equal(plain(value.chatId), CHAT)
  assert.equal(plain(value.verbosity), 'final')
  assert.equal(plain(value.streaming), true)
  assert.throws(() => schema({ verbosity: 'loud' }))
  return undefined
})

test('plugin boots, polls, and stops polling on dispose', async () => {
  const env = await bootPlugin()
  assert.ok(env.tg.callsOf('getMe').length >= 1)
  assert.ok(env.tg.callsOf('setMyCommands').length >= 1)
  await env.dsh.dispose()
  const count = env.tg.callsOf('getUpdates').length
  await sleep(400)
  assert.equal(env.tg.callsOf('getUpdates').length, count, 'no polling after dispose')
  await env.tg.close()
})

test('without a token the plugin waits, reports no-token, and does not call Telegram', async (t) => {
  const env = await bootPlugin({ token: null, waitForPoll: false })
  t.after(env.close)
  await sleep(300)
  assert.equal(env.tg.calls.length, 0)
  const status = await env.dsh.request(ROUTES.status)
  assert.equal(status.body.poller, 'no-token')
  assert.equal(status.body.token.configured, false)
  // Saving a token starts polling without a restart.
  const saved = await env.dsh.request(ROUTES.token, { method: 'POST', body: { token: TOKEN } })
  assert.equal(saved.body.ok, true)
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'getUpdates'), 3000)
})

test('end to end: DSH session events reach the topic and topic text reaches sessionController.prompt', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const { dsh, tg, session } = env
  dsh.emit('session/event', session, { type: 'user/message', data: { content: [{ type: 'text', text: 'Fix the login bug' }], source: { kind: 'user', rpcId: 'gui-1' } } })
  dsh.emit('session/event', session, { type: 'turn/start', data: { turn: 0 } })
  dsh.emit('agent/assistant-stream', { agent: env.agent, frame: { type: 'start', attemptId: 'a1', turn: 0, step: 0 } })
  dsh.emit('agent/assistant-stream', { agent: env.agent, frame: { type: 'chunk', attemptId: 'a1', index: 0, chunk: { type: 'text-delta', index: 0, text: 'Looking ' } } })
  dsh.emit('agent/assistant-stream', { agent: env.agent, frame: { type: 'chunk', attemptId: 'a1', index: 1, chunk: { type: 'text-delta', index: 0, text: 'into it.' } } })
  dsh.emit('session/event', session, { type: 'assistant/message', data: { message: { content: [{ type: 'text', text: 'Looking into it.' }, { type: 'tool-call', id: 'c1', name: 'bash', arguments: '{"command":"npm test"}' }] } } })
  dsh.emit('session/event', session, { type: 'tool/result', data: { message: { toolCallId: 'c1', content: [{ type: 'text', text: 'ok' }] } } })
  dsh.emit('session/event', session, { type: 'turn/end', data: { turn: 0, reason: { kind: 'completed' } } })

  await tg.waitFor((calls) => calls.some((call) => call.method === 'sendMessage' && /✅ Done/.test(call.params.text)), 8000)
  const topic = tg.callsOf('createForumTopic')
  assert.equal(topic.length, 1)
  assert.equal(topic[0].params.name, 'codex-tg · Title of session-a')
  const thread = 101
  const texts = tg.calls.filter((call) => ['sendMessage', 'editMessageText'].includes(call.method)).map((call) => call.params.text)
  assert.ok(texts.some((text) => /👤.*\n?Fix the login bug/s.test(text)))
  assert.ok(texts.some((text) => text === 'Looking into it.'))
  assert.ok(texts.some((text) => /🛠 1 tool/.test(text) && /npm test/.test(text)))
  for (const call of tg.callsOf('sendMessage').filter((c) => c.params.message_thread_id !== undefined)) assert.equal(call.params.message_thread_id, thread)

  // Telegram -> DSH
  tg.pushUpdate(topicMessage(thread, 'now add a test'))
  await tg.waitFor(() => dsh.prompts.length > 0, 5000)
  const prompt = dsh.prompts[0]
  assert.equal(prompt.sessionId, 'session-a')
  assert.equal(prompt.mode, 'queue')
  assert.deepEqual(prompt.content, [{ type: 'text', text: 'now add a test' }])
  assert.match(prompt.requestId, /^tg-/)

  // The resulting user/message is not echoed back.
  const before = tg.callsOf('sendMessage').length
  dsh.emit('session/event', session, { type: 'user/message', data: { content: [{ type: 'text', text: 'now add a test' }], source: { kind: 'user', rpcId: prompt.requestId } } })
  await sleep(300)
  assert.equal(tg.callsOf('sendMessage').filter((call) => /now add a test/.test(call.params.text)).length, 0)
  assert.ok(tg.callsOf('sendMessage').length >= before)
})

test('subagent activity never creates a topic and shows one line in the root topic', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const { dsh, tg, session } = env
  dsh.emit('session/event', session, { type: 'turn/start', data: { turn: 0 } })
  const { session: child, agent: childAgent } = dsh.addSession('child-1', { origin: 'subagent', parentSession: 'session-a' })
  dsh.emit('agent/created', { agent: childAgent, source: 'startup' })
  dsh.emit('subagent/start', { id: 'child-1', provider: 'spawn', runId: 'r' })
  dsh.emit('session/event', session, { type: 'subagent/catalog', data: { childId: 'child-1', label: 'Audit the parser', mode: 'one-shot' } })
  dsh.emit('session/event', child, { type: 'user/message', data: { content: [{ type: 'text', text: 'child task' }], source: { kind: 'user' } } })
  dsh.emit('session/event', child, { type: 'assistant/message', data: { message: { content: [{ type: 'text', text: 'child says hi' }] } } })
  dsh.emit('agent/assistant-stream', { agent: childAgent, frame: { type: 'start', attemptId: 'c', turn: 0, step: 0 } })
  await sleep(600)
  dsh.emit('subagent/end', { id: 'child-1', stopReason: 'completed' })
  await tg.waitFor((calls) => calls.some((call) => call.method === 'editMessageText' && /Audit the parser.*done/.test(call.params.text)), 8000)
  assert.equal(tg.callsOf('createForumTopic').length, 1)
  const texts = tg.calls.map((call) => call.params?.text ?? '')
  assert.equal(texts.some((text) => /child says hi|child task/.test(text)), false)
  assert.equal(tg.callsOf('sendMessage').filter((call) => /Subagent/.test(call.params.text)).length, 1)
})

test('settings routes: status, token, test, bind code, and saved binding', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const { dsh, tg } = env
  const status = await dsh.request(ROUTES.status)
  assert.equal(status.body.ok, true)
  assert.equal(status.body.token.configured, true)
  assert.equal(status.body.bot.username, 'fake_bot')
  assert.equal(JSON.stringify(status.body).includes(TOKEN), false, 'status never contains the token')

  const bad = await dsh.request(ROUTES.token, { method: 'POST', body: { token: 'nope' } })
  assert.equal(bad.body.ok, false)
  const good = await dsh.request(ROUTES.token, { method: 'POST', body: { token: '987654321:BBEexampleexampleexampleexampleexb' } })
  assert.equal(good.body.ok, true)
  assert.equal(dsh.credentialStore.get('DSH_TELEGRAM_BOT_TOKEN'), '987654321:BBEexampleexampleexampleexampleexb')

  const test = await dsh.request(ROUTES.test, { method: 'POST' })
  assert.equal(test.body.ok, true)
  assert.equal(test.body.chat.isForum, true)
  assert.equal(test.body.canManageTopics, true)

  const bind = await dsh.request(ROUTES.bind, { method: 'POST' })
  assert.match(bind.body.code, /^[a-z0-9]{6,}$/)
  tg.pushUpdate({ message: { message_id: 77, from: { id: 31337 }, chat: { id: -100777, type: 'supergroup', is_forum: true }, text: `/bind ${bind.body.code}` } })
  await tg.waitFor(() => dsh.settingsWrites.length > 0, 5000)
  const ops = dsh.settingsWrites[0].ops
  assert.deepEqual(ops, [{ op: 'set', path: ['chatId'], value: -100777 }, { op: 'set', path: ['allowedUserIds'], value: [OWNER, 31337] }])

  const session = await dsh.request(`${ROUTES.session}`)
  assert.equal(session.body.linked, false)

  const wrong = await dsh.request(ROUTES.test, { method: 'GET' })
  assert.equal(wrong.status, 405)
})

test('routes reject unauthenticated requests', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  env.dsh.services.connection.requestRejection = () => 401
  const result = await env.dsh.request(ROUTES.status)
  assert.equal(result.status, 401)
  assert.equal(result.body, undefined)
})
