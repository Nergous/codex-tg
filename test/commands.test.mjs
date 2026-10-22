import assert from 'node:assert/strict'
import { test } from 'node:test'
import { CHAT, OWNER, makeBridge, settle, topicMessage } from './harness.mjs'

async function env0(t, options) {
  const env = await makeBridge(options)
  t.after(env.close)
  await env.store.putTopic('session-a', { chatId: CHAT, threadId: 300, name: 'ws · t', closed: false })
  env.host.sessions.set('session-a', { cwd: '/home/u/codex-tg', workspaceTitle: 'codex-tg', title: 'Fix login', model: 'Model One', modelId: 'p/m1' })
  return env
}

const lastText = (env) => env.tg.callsOf('sendMessage').at(-1)?.params.text

function generalMessage(text) {
  return { message: { message_id: 9000 + Math.floor(Math.random() * 1000), from: { id: OWNER }, chat: { id: CHAT, type: 'supergroup', is_forum: true }, text } }
}

test('/stop cancels the running turn', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/stop'))
  assert.deepEqual(env.host.calls.map((call) => call.op), ['cancel'])
  assert.match(lastText(env), /Stop requested/)
})

test('/status shows workspace, model, context and output mode', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/status@fake_bot'))
  const text = lastText(env)
  assert.match(text, /codex-tg/)
  assert.match(text, /Model One/)
  assert.match(text, /Context: 12\.0k \/ 200\.0k/)
  assert.match(text, /Output: tools/)
})

test('/diff sends the stat and the patch as a file', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/diff'))
  await settle(env)
  assert.match(lastText(env), /a\.js \| 2 \+-/)
  const doc = env.tg.callsOf('sendDocument')[0]
  assert.equal(doc.params.document.name, 'changes.diff')
  assert.equal(Number(doc.params.message_thread_id), 300)
})

test('/compact runs compaction and reports', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/compact'))
  assert.ok(env.host.calls.some((call) => call.op === 'compact'))
  assert.match(lastText(env), /Compacted 5000 tokens/)
})

test('/compact failure is reported, not thrown', async (t) => {
  const env = await env0(t)
  env.host.compact = async () => { throw new Error('the session is busy') }
  await env.bridge.handleUpdate(topicMessage(300, '/compact'))
  assert.match(lastText(env), /\/compact failed: the session is busy/)
})

test('/model lists models as buttons; a button press switches', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/model'))
  const call = env.tg.callsOf('sendMessage').at(-1)
  const keyboard = call.params.reply_markup.inline_keyboard
  assert.equal(keyboard.length, 2)
  assert.match(keyboard[0][0].text, /Model One ✓/)
  await env.bridge.handleUpdate({ callback_query: { id: 'cb1', from: { id: OWNER }, message: { message_id: 1, chat: { id: CHAT } }, data: keyboard[1][0].callback_data } })
  assert.deepEqual(env.host.calls.at(-1), { op: 'setModel', id: 'session-a', target: 'p/m2' })
  await settle(env)
  assert.match(lastText(env), /Model set to <b>Model Two<\/b>/)
})

test('/model <name> switches directly', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/model m2'))
  assert.deepEqual(env.host.calls.at(-1), { op: 'setModel', id: 'session-a', target: 'm2' })
})

test('/verbosity changes the per-topic output mode', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/verbosity final'))
  assert.equal(env.store.topic('session-a').verbosity, 'final')
  await env.bridge.handleUpdate(topicMessage(300, '/verbosity nonsense'))
  assert.match(lastText(env), /Output detail: <b>final<\/b>/)
})

test('/mute and /unmute toggle mirroring', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/mute'))
  assert.equal(env.store.topic('session-a').muted, true)
  await env.bridge.handleUpdate(topicMessage(300, '/unmute'))
  assert.equal(env.store.topic('session-a').muted, false)
})

test('/steer sends a steering message', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/steer use pnpm'))
  const send = env.host.calls.at(-1)
  assert.equal(send.op, 'sendInput')
  assert.equal(send.options.mode, 'steer')
  assert.equal(send.input.text, 'use pnpm')
})

test('/help in a topic lists topic commands', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/help'))
  assert.match(lastText(env), /\/stop/)
  assert.match(lastText(env), /\/diff/)
})

test('DSH slash commands such as /goal run through the DSH command registry', async (t) => {
  const env = await env0(t)
  const ran = []
  env.host.runCommand = async (id, line) => {
    ran.push([id, line])
    return { ok: true, text: 'Goal set.' }
  }
  await env.bridge.handleUpdate(topicMessage(300, '/goal@fake_bot finish the migration'))
  assert.deepEqual(ran, [['session-a', '/goal finish the migration']])
  assert.match(lastText(env), /⚙️ \/goal\nGoal set\./)
  assert.equal(env.host.calls.filter((call) => call.op === 'sendInput').length, 0)
})

test('slash text DSH does not know is passed to the session as a message', async (t) => {
  const env = await env0(t)
  env.host.runCommand = async () => undefined
  await env.bridge.handleUpdate(topicMessage(300, '/whatever finish the migration'))
  const send = env.host.calls.at(-1)
  assert.equal(send.op, 'sendInput')
  assert.equal(send.input.text, '/whatever finish the migration')
})

test('General: /help, /sessions with topic links, /workspaces', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(generalMessage('/help'))
  assert.match(lastText(env), /\/sessions/)
  await env.bridge.handleUpdate(generalMessage('/sessions'))
  assert.match(lastText(env), /<a href="https:\/\/t\.me\/c\/1234567890\/300">Fix login<\/a>/)
  await env.bridge.handleUpdate(generalMessage('/workspaces'))
  assert.match(lastText(env), /codex-tg.*\/home\/u\/codex-tg/)
})

test('General: /new starts a session in a workspace with a first prompt and opens its topic', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(generalMessage('/new codex write a README'))
  const created = env.host.calls.find((call) => call.op === 'newSession')
  assert.deepEqual(created, { op: 'newSession', path: '/home/u/codex-tg', first: { text: 'write a README' } })
  await settle(env)
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
  assert.ok(env.store.topic('session-new'))
  assert.ok(env.tg.callsOf('sendMessage').some((call) => /Session started in <b>codex-tg<\/b>.*open topic/.test(call.params.text)))
})

test('General: /new with an unknown workspace explains', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(generalMessage('/new nope'))
  assert.match(lastText(env), /Unknown workspace/)
})

test('General: plain text points to topics', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(generalMessage('hello'))
  assert.match(lastText(env), /Write inside a session topic/)
})
