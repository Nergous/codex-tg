import assert from 'node:assert/strict'
import { test } from 'node:test'
import { CHAT, OWNER, makeBridge, settle, topicMessage } from './harness.mjs'

async function bound(env) {
  await env.store.putTopic('session-a', { chatId: CHAT, threadId: 300, name: 'ws · t', closed: false })
  await env.store.putTopic('session-b', { chatId: CHAT, threadId: 301, name: 'ws · u', closed: false })
}

test('text in a topic goes to that topic session as a queued prompt', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  await env.bridge.handleUpdate(topicMessage(301, 'run the tests'))
  await settle(env)
  const send = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(send.id, 'session-b')
  assert.equal(send.input.text, 'run the tests')
  assert.equal(send.options.mode, 'followup')
  assert.equal(env.tg.callsOf('setMessageReaction').length, 0, 'no read reactions')
})

test('the steer prefix steers the running turn', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  await env.bridge.handleUpdate(topicMessage(300, '!focus on the API first'))
  const send = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(send.options.mode, 'steer')
  assert.equal(send.input.text, 'focus on the API first')
})

test('messages from other users, other chats, or bots are ignored', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  await env.bridge.handleUpdate(topicMessage(300, 'hi', { from: { id: 999, is_bot: false } }))
  await env.bridge.handleUpdate(topicMessage(300, 'hi', { chat: { id: -100999, type: 'supergroup' } }))
  await env.bridge.handleUpdate({ message: { message_id: 1, chat: { id: OWNER, type: 'private' }, from: { id: OWNER }, text: 'hi' } })
  await settle(env)
  assert.equal(env.host.calls.length, 0)
  assert.equal(env.tg.callsOf('sendMessage').length, 0)
})

test('an unknown topic gets a notice instead of a delivery', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  await env.bridge.handleUpdate(topicMessage(999, 'hello?'))
  await settle(env)
  assert.equal(env.host.calls.length, 0)
  assert.match(env.tg.callsOf('sendMessage')[0].params.text, /not linked/)
})

test('a delivery failure is reported in the topic', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  env.host.sendInput = async () => { throw new Error('session/agent-busy') }
  await env.bridge.handleUpdate(topicMessage(300, 'x'))
  await settle(env)
  assert.match(env.tg.callsOf('sendMessage')[0].params.text, /Not delivered: session\/agent-busy/)
})

test('no reactions are set on inbound messages, at delivery or at turn end', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  await env.bridge.handleUpdate(topicMessage(300, 'go'))
  await env.bridge.handleUpdate(topicMessage(300, '!faster'))
  env.bridge.onTurnStarted('session-a')
  env.bridge.onTurnEnded('session-a', { reason: 'completed' })
  await settle(env, 'session-a')
  assert.equal(env.tg.callsOf('setMessageReaction').length, 0)
})

test('/bind with the code from DSH binds a forum supergroup and allows the sender', async (t) => {
  const env = await makeBridge({ bound: false })
  t.after(env.close)
  const { code } = env.bridge.createBindCode()
  const message = (text, chat) => ({ message: { message_id: 1, from: { id: 4242, is_bot: false }, chat, text } })
  await env.bridge.handleUpdate(message('/bind wrong', { id: -100555, type: 'supergroup', is_forum: true }))
  assert.equal(env.bindings.length, 0)
  await env.bridge.handleUpdate(message(`/bind ${code}`, { id: -100555, type: 'supergroup', is_forum: false }))
  assert.equal(env.bindings.length, 0)
  assert.match(env.tg.callsOf('sendMessage').at(-1).params.text, /Enable Topics/)
  await env.bridge.handleUpdate(message(`/bind@fake_bot ${code}`, { id: -100555, type: 'supergroup', is_forum: true }))
  assert.deepEqual(env.bindings, [{ chatId: -100555, allowedUserIds: [4242] }])
  assert.equal(env.bridge.bindCode, undefined)
  await env.bridge.handleUpdate(message(`/bind ${code}`, { id: -100666, type: 'supergroup', is_forum: true }))
  assert.equal(env.bindings.length, 1, 'a code works once')
})

test('a text reply answers an open question instead of becoming a prompt', async (t) => {
  const env = await makeBridge()
  t.after(env.close)
  await bound(env)
  const prompt = await env.bridge.requestQuestions('session-a', [{ id: 'q1', question: 'Which DB?', options: [{ label: 'Postgres' }, { label: 'SQLite' }] }])
  await settle(env)
  await env.bridge.handleUpdate(topicMessage(300, 'MySQL please'))
  const answers = await prompt.answer
  assert.deepEqual(answers, [{ id: 'q1', selected: [], custom: 'MySQL please' }])
  assert.equal(env.host.calls.filter((call) => call.op === 'sendInput').length, 0)
})
