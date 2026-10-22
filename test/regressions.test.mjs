/** Regression tests for issues found in code review. */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { resolveConfig, ROUTES } from '../index.js'
import { compactArgs } from '../lib/host.js'
import { TelegramError } from '../lib/telegram/api.js'
import { runPoller } from '../lib/telegram/poller.js'
import { SendQueue } from '../lib/telegram/queue.js'
import { BridgeStore } from '../lib/store.js'
import { CHAT, OWNER, makeBridge, settle, sleep, topicMessage } from './harness.mjs'
import { bootPlugin, press } from './plugin-env.mjs'

async function withTopic(env, sessionId = 'session-a', threadId = 300) {
  await env.store.putTopic(sessionId, { chatId: CHAT, threadId, name: 'ws · t', closed: false })
  return threadId
}

test('a Telegram message mid-stream does not duplicate the answer or leave the cursor', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onTurnStarted('session-a')
  env.bridge.onAssistantDelta('session-a', 'a1', 'Part one')
  await settle(env, 'session-a')
  // steer from Telegram while streaming; DSH logs it as a telegram user message
  await env.bridge.handleUpdate(topicMessage(300, '!faster please'))
  env.bridge.onUserMessage('session-a', { text: 'faster please', source: { kind: 'telegram' } })
  await settle(env, 'session-a')
  env.bridge.onAssistantDelta('session-a', 'a1', 'Part one and part two')
  await sleep(200)
  env.bridge.onAssistantMessage('session-a', { attemptKey: 'a1', text: 'Part one and part two, done.', toolCalls: [] })
  env.bridge.onTurnEnded('session-a', { reason: 'completed' })
  await settle(env, 'session-a')
  const sends = env.tg.callsOf('sendMessage').filter((call) => /Part one/.test(call.params.text))
  assert.equal(sends.length, 1, 'the answer is one message, edited in place')
  const messageId = [...env.tg.messages.values()].find((m) => /Part one/.test(m.text))?.message_id
  assert.ok(messageId)
  const finalEdit = env.tg.callsOf('editMessageText').filter((call) => call.params.message_id === messageId).at(-1)
  assert.equal(finalEdit.params.text, 'Part one and part two, done.')
  assert.equal(env.tg.calls.some((call) => /▍/.test(call.params?.text ?? '') && call === env.tg.calls.filter((c) => c.params?.message_id === messageId).at(-1)), false)
})

test('a sealed segment does not show the streaming cursor', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  env.bridge.onAssistantDelta('session-a', 'a1', 'Streaming text')
  await settle(env, 'session-a')
  env.bridge.onUserMessage('session-a', { text: 'interrupt', source: { kind: 'user' } })
  await settle(env, 'session-a')
  const last = env.tg.calls.filter((call) => /Streaming text/.test(call.params?.text ?? '')).at(-1)
  assert.equal(last.params.text, 'Streaming text')
})

test('the GUI approval card is shown at once, not after Telegram topic creation', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  env.tg.slowNext('createForumTopic', 600)
  let guiCalledAt
  const started = Date.now()
  const outcome = env.dsh.waterfall('approval/request', { agent: env.agent, toolName: 'bash', signal: new AbortController().signal }, () => {
    guiCalledAt = Date.now()
    return Promise.resolve('rejected')
  })
  assert.equal(await outcome, 'rejected')
  assert.ok(guiCalledAt - started < 100, `GUI waited ${guiCalledAt - started} ms`)
  // the Telegram prompt is still posted and then marked answered in DSH
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'editMessageText' && /answered in DSH/.test(call.params.text)), 5000)
})

test('when the Telegram prompt cannot be delivered, a GUI "unavailable" is returned', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  // topic exists; the prompt send fails for good
  for (let i = 0; i < 2; i += 1) env.tg.failNext('sendMessage', { error_code: 403, description: 'Forbidden: bot was kicked from the supergroup chat' })
  const result = await Promise.race([
    env.dsh.waterfall('approval/request', { agent: env.agent, toolName: 'bash', signal: new AbortController().signal }, () => Promise.resolve('unavailable')),
    sleep(4000).then(() => 'hung'),
  ])
  assert.equal(result, 'unavailable')
})

test('a topic deleted in Telegram is recreated and the message re-sent', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env, 'session-a', 300)
  env.tg.failNext('sendMessage', { error_code: 400, description: 'Bad Request: message thread not found' })
  env.bridge.onUserMessage('session-a', { text: 'are you there', source: { kind: 'user' } })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
  const record = env.store.topic('session-a')
  assert.notEqual(record.threadId, 300)
  const delivered = env.tg.callsOf('sendMessage').filter((call) => /are you there/.test(call.params.text))
  assert.equal(delivered.at(-1).params.message_thread_id, record.threadId)
})

test('a topic closed in Telegram is reopened and the message re-sent', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env, 'session-a', 300)
  env.tg.failNext('sendMessage', { error_code: 400, description: 'Bad Request: TOPIC_CLOSED' })
  env.bridge.onUserMessage('session-a', { text: 'hello again', source: { kind: 'user' } })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('reopenForumTopic').length, 1)
  assert.equal(env.tg.callsOf('sendMessage').filter((call) => /hello again/.test(call.params.text)).length, 2)
})

test('closing the bridge releases a pending approval to the GUI result', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await withTopic(env)
  const prompt = await env.bridge.requestApproval('session-a', { toolName: 'bash' })
  assert.ok(prompt)
  env.bridge.close()
  assert.equal(await prompt.answer, undefined)
})

test('approval buttons latch on the first press', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const outcome = env.dsh.waterfall('approval/request', { agent: env.agent, toolName: 'bash', signal: new AbortController().signal }, () => new Promise(() => {}))
  const target = await press(env, /Allow/)
  env.tg.pushUpdate({ callback_query: { id: 'cb-second', from: { id: OWNER }, message: { message_id: target.messageId, chat: { id: CHAT } }, data: target.data.replace(/:a$/, ':r') } })
  assert.equal(await outcome, 'allowed-once')
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'answerCallbackQuery' && call.params.callback_query_id === 'cb-second'), 3000)
  const second = env.tg.callsOf('answerCallbackQuery').find((call) => call.params.callback_query_id === 'cb-second')
  assert.match(second.params.text, /Already answered|expired/)
})

test('subagent approvals without a resolvable root never create a topic', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const orphan = { id: 'orphan', session: { id: 'orphan', header: { origin: 'subagent', parentSession: 'gone' } } }
  const outcome = await env.dsh.waterfall('approval/request', { agent: orphan, toolName: 'bash', signal: new AbortController().signal }, () => Promise.resolve('rejected'))
  assert.equal(outcome, 'rejected')
  await sleep(200)
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
})

test('createTopicOn never: approvals do not create topics', async (t) => {
  const env = await bootPlugin({ config: { createTopicOn: 'never' } })
  t.after(env.close)
  const outcome = await env.dsh.waterfall('approval/request', { agent: env.agent, toolName: 'bash', signal: new AbortController().signal }, () => Promise.resolve('unavailable'))
  assert.equal(outcome, 'unavailable')
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
})

test('anonymous admins, channel posts and bots are not treated as allowed users', async (t) => {
  const env = await makeBridge({ settings: { allowedUserIds: [OWNER, 1087968824, 136817688] } })
  t.after(env.close)
  await withTopic(env)
  await env.bridge.handleUpdate(topicMessage(300, 'as anon admin', { from: { id: 1087968824, is_bot: true, first_name: 'Group' }, sender_chat: { id: CHAT, type: 'supergroup' } }))
  await env.bridge.handleUpdate(topicMessage(300, 'as channel', { from: { id: 136817688, is_bot: true }, sender_chat: { id: -100999, type: 'channel' } }))
  assert.equal(env.host.calls.length, 0)
})

test('/bind refuses anonymous senders', async (t) => {
  const env = await makeBridge({ bound: false })
  t.after(env.close)
  const { code } = env.bridge.createBindCode()
  await env.bridge.handleUpdate({ message: { message_id: 1, from: { id: 1087968824, is_bot: true }, sender_chat: { id: -100555, type: 'supergroup' }, chat: { id: -100555, type: 'supergroup', is_forum: true }, text: `/bind ${code}` } })
  assert.equal(env.bindings.length, 0)
  assert.ok(env.bridge.bindCode, 'the code stays valid for a real user')
})

test('config cannot point the token at another credential or the API at a foreign http host', () => {
  assert.equal(resolveConfig({ tokenRef: 'OPENAI_API_KEY' }).tokenRef, 'DSH_TELEGRAM_BOT_TOKEN')
  assert.equal(resolveConfig({ tokenRef: 'MY_TELEGRAM_BOT' }).tokenRef, 'MY_TELEGRAM_BOT')
  assert.equal(resolveConfig({ apiBaseUrl: 'http://evil.example' }).apiBaseUrl, 'https://api.telegram.org')
  assert.equal(resolveConfig({ apiBaseUrl: 'https://user:pw@evil.example' }).apiBaseUrl, 'https://api.telegram.org')
  assert.equal(resolveConfig({ apiBaseUrl: 'http://127.0.0.1:8081' }).apiBaseUrl, 'http://127.0.0.1:8081')
  assert.equal(resolveConfig({ apiBaseUrl: 'https://tg.example/' }).apiBaseUrl, 'https://tg.example/')
})

test('the settings route ignores tokenRef and apiBaseUrl', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const result = await env.dsh.request(ROUTES.settings, { method: 'POST', body: { patch: { tokenRef: 'OTHER_TELEGRAM', apiBaseUrl: 'https://x.example', verbosity: 'final' } } })
  assert.equal(result.body.ok, true)
  assert.deepEqual(env.dsh.settingsWrites.at(-1).ops, [{ op: 'set', path: ['verbosity'], value: 'final' }])
})

test('the poller survives a failing offset store and skips already-seen updates', async () => {
  const seen = []
  const controller = new AbortController()
  let round = 0
  const api = {
    call: async () => {
      round += 1
      if (round > 3) {
        controller.abort()
        return []
      }
      return [{ update_id: 10, message: { text: 'a' } }, { update_id: 11, message: { text: 'b' } }]
    },
  }
  await runPoller({
    api,
    signal: controller.signal,
    getOffset: () => { throw new Error('storage closed') },
    setOffset: async () => { throw new Error('disk full') },
    onUpdate: (update) => { seen.push(update.update_id) },
    onState: () => { throw new Error('reporting bug') },
  })
  assert.deepEqual(seen, [10, 11])
})

test('a timed-out sendMessage is not retried (no duplicates); refused connections are', async () => {
  const queue = new SendQueue({ perMinute: 1000, minGapMs: 0, maxRetries: 2 })
  let attempts = 0
  await assert.rejects(queue.push(async () => {
    attempts += 1
    throw new TelegramError('sendMessage', 0, 'request timed out')
  }, { idempotent: false }))
  assert.equal(attempts, 1)
  attempts = 0
  const result = await queue.push(async () => {
    attempts += 1
    if (attempts === 1) throw new TelegramError('sendMessage', 0, 'network error: ECONNREFUSED')
    return 'ok'
  }, { idempotent: false })
  assert.equal(result, 'ok')
  queue.close()
})

test('topic creation waits for storage to open', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  let open
  env.bridge.ready = new Promise((resolve) => { open = resolve })
  env.bridge.onSessionActive('session-z')
  await sleep(150)
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
  // storage opens with an existing record: no new topic
  const persisted = new BridgeStore()
  await persisted.putTopic('session-z', { chatId: CHAT, threadId: 42, name: 'x', closed: false })
  env.store.domain = persisted.domain
  open()
  await settle(env, 'session-z')
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
})

test('messages of one topic reach the session in order even when the first is slow', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  env.dsh.services.fileUploads = {
    upload: async () => {
      await sleep(400)
      return { receiptId: 'r1' }
    },
  }
  env.tg.addFile('doc-slow', Buffer.from('data'))
  env.dsh.emit('session/event', env.session, { type: 'turn/start', data: { turn: 0 } })
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'createForumTopic'), 5000)
  await sleep(100)
  env.tg.pushUpdate(topicMessage(101, undefined, { caption: 'first', document: { file_id: 'doc-slow', file_name: 'a.txt', mime_type: 'text/plain', file_size: 4 } }))
  env.tg.pushUpdate(topicMessage(101, 'second'))
  await env.tg.waitFor(() => env.dsh.prompts.length >= 2, 5000)
  assert.deepEqual(env.dsh.prompts.map((prompt) => prompt.content[0].text), ['first', 'second'])
})

test('tracked tool arguments keep previews, not bulky content', () => {
  const big = 'x'.repeat(100000)
  assert.deepEqual(compactArgs({ file_path: 'a.js', content: big }), { file_path: 'a.js' })
  assert.deepEqual(compactArgs({ files: [{ path: 'r.md', description: 'Report' }] }), { files: [{ path: 'r.md', description: 'Report' }] })
  assert.ok(compactArgs({ blob: big }).length <= 500)
})
