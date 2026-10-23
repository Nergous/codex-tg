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

test('/permission shows access buttons; a press switches the session', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/permission'))
  const call = env.tg.callsOf('sendMessage').at(-1)
  assert.match(call.params.text, /Access: <b>✏️ Workspace Write<\/b>/)
  const buttons = call.params.reply_markup.inline_keyboard.flat()
  assert.deepEqual(buttons.map((b) => b.text), ['👁 Read Only', '✏️ Workspace Write ✓', '⚠️ Full Access'])
  await env.bridge.handleUpdate({ callback_query: { id: 'cb-p', from: { id: OWNER }, message: { message_id: 1, chat: { id: CHAT } }, data: buttons[0].callback_data } })
  assert.deepEqual(env.host.calls.at(-1), { op: 'setPermission', id: 'session-a', preset: 'read-only' })
  await settle(env)
  assert.match(lastText(env), /Access set to <b>👁 Read Only<\/b>/)
})

test('/access <alias> switches directly; unknown names are explained', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(topicMessage(300, '/access full'))
  assert.deepEqual(env.host.calls.at(-1), { op: 'setPermission', id: 'session-a', preset: 'danger-full-access' })
  await env.bridge.handleUpdate(topicMessage(300, '/permission Workspace Write'))
  assert.deepEqual(env.host.calls.at(-1), { op: 'setPermission', id: 'session-a', preset: 'workspace-write' })
  await env.bridge.handleUpdate(topicMessage(300, '/permission root'))
  assert.match(lastText(env), /Unknown access/)
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

test('General: /help and /workspaces', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(generalMessage('/help'))
  assert.match(lastText(env), /\/sessions/)
  await env.bridge.handleUpdate(generalMessage('/workspaces'))
  assert.match(lastText(env), /codex-tg.*\/home\/u\/codex-tg/)
})

test('General: /sessions links sessions with a topic and creates a topic for one without', async (t) => {
  const env = await env0(t)
  env.host.sessions.set('session-old', { cwd: '/home/u/codex-tg', workspaceTitle: 'codex-tg', title: 'Old refactor' })
  await env.bridge.handleUpdate(generalMessage('/sessions'))
  const list = env.tg.callsOf('sendMessage').at(-1)
  assert.match(list.params.text, /Recent sessions/)
  const rows = list.params.reply_markup.inline_keyboard.map((row) => row[0])
  const linked = rows.find((b) => /Fix login/.test(b.text))
  assert.equal(linked.url, 'https://t.me/c/1234567890/300', 'a session with a topic opens it directly')
  assert.match(linked.text, /^🧵 Fix login · codex-tg/)
  const missing = rows.find((b) => /Old refactor/.test(b.text))
  assert.equal(missing.url, undefined)
  assert.match(missing.text, /^➕ /)

  await env.bridge.handleUpdate(press(missing.callback_data, OWNER, 'cb-open'))
  await settle(env)
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
  const record = env.store.topic('session-old')
  assert.ok(record, 'the old session now has a topic')
  assert.match(env.tg.callsOf('createForumTopic')[0].params.name, /Old refactor/)
  const link = `https://t.me/c/1234567890/${record.threadId}`
  assert.ok(env.tg.callsOf('sendMessage').some((call) => call.params.message_thread_id === undefined && call.params.text.includes(`<a href="${link}">Open topic</a>`)), 'the link is posted in General')
  assert.match(env.tg.callsOf('answerCallbackQuery').find((call) => call.params.callback_query_id === 'cb-open').params.text, /Topic ready/)
  const updated = env.tg.callsOf('editMessageReplyMarkup').at(-1).params.reply_markup.inline_keyboard.map((row) => row[0])
  assert.equal(updated.find((b) => /Old refactor/.test(b.text)).url, link, 'the button turns into a link')

  // Pressing again (stale keyboard) reuses the topic.
  await env.bridge.handleUpdate(press(missing.callback_data))
  await settle(env)
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
})

test('General: /sessions reopens a closed topic instead of linking to it', async (t) => {
  const env = await env0(t)
  await env.store.patchTopic('session-a', { closed: true })
  await env.bridge.handleUpdate(generalMessage('/sessions'))
  const button = env.tg.callsOf('sendMessage').at(-1).params.reply_markup.inline_keyboard[0][0]
  assert.equal(button.url, undefined)
  await env.bridge.handleUpdate(press(button.callback_data))
  await settle(env)
  assert.equal(env.tg.callsOf('reopenForumTopic').length, 1)
  assert.equal(env.tg.callsOf('createForumTopic').length, 0)
  assert.equal(env.store.topic('session-a').closed, false)
})

test('General: /new starts a session in a workspace with a first prompt and opens its topic', async (t) => {
  const env = await env0(t)
  await env.bridge.handleUpdate(generalMessage('/new codex write a README'))
  const created = env.host.calls.find((call) => call.op === 'newSession')
  assert.deepEqual(created, { op: 'newSession', path: '/home/u/codex-tg', first: undefined })
  const input = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(input.id, 'session-new')
  assert.equal(input.input.text, 'write a README')
  await settle(env, 'session-new')
  assert.equal(env.tg.callsOf('createForumTopic').length, 1)
  const thread = env.store.topic('session-new').threadId
  assert.ok(env.tg.callsOf('sendMessage').some((call) => call.params.message_thread_id === thread && /You \(Telegram\)<\/b>\nwrite a README/.test(call.params.text)), 'the prompt is echoed in the new topic')
  assert.ok(env.tg.callsOf('sendMessage').some((call) => /Session started in <b>codex-tg<\/b>.*open topic/.test(call.params.text)))
})

const WORKSPACES = [
  { id: 'ws1', title: 'codex-tg', path: '/home/u/codex-tg' },
  { id: 'ws2', title: 'site', path: '/home/u/site' },
  { id: 'ws3', title: 'infra', path: '/home/u/infra' },
]

function press(data, from = OWNER, id = `cb-${Math.random()}`) {
  return { callback_query: { id, from: { id: from }, message: { message_id: 1, chat: { id: CHAT } }, data } }
}

const buttonsOf = (call) => (call.params.reply_markup?.inline_keyboard ?? []).flat()

test('General: /new without arguments lists workspaces as buttons, then takes the next message as the prompt', async (t) => {
  const env = await env0(t)
  env.host.listWorkspaces = async () => WORKSPACES
  await env.bridge.handleUpdate(generalMessage('/new'))
  const picker = env.tg.callsOf('sendMessage').at(-1)
  assert.match(picker.params.text, /Pick a workspace/)
  const buttons = buttonsOf(picker)
  assert.deepEqual(buttons.map((b) => b.text), ['codex-tg', 'site', 'infra', '✖ Cancel'])
  assert.equal(env.host.calls.some((call) => call.op === 'newSession'), false, 'nothing starts before a choice')

  await env.bridge.handleUpdate(press(buttons[1].callback_data))
  await settle(env)
  const edit = env.tg.callsOf('editMessageText').at(-1)
  assert.match(edit.params.text, /New session in site/)
  assert.match(edit.params.text, /Send the first message/)
  assert.ok(buttonsOf(edit).some((b) => /Start without a message/.test(b.text)))

  await env.bridge.handleUpdate(generalMessage('fix the footer'))
  await settle(env, 'session-new')
  assert.deepEqual(env.host.calls.find((call) => call.op === 'newSession'), { op: 'newSession', path: '/home/u/site', first: undefined })
  const input = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(input.id, 'session-new')
  assert.equal(input.input.text, 'fix the footer')
  assert.match(env.tg.callsOf('editMessageText').at(-1).params.text, /Session started in <b>site<\/b>.*open topic/)
  assert.equal(env.bridge.newFlows.size, 0)

  // The flow is over: the next General text gets the usual hint.
  await env.bridge.handleUpdate(generalMessage('hello again'))
  assert.match(lastText(env), /Write inside a session topic/)
})

test('General: /new <workspace> without a prompt asks for the first message; a photo works as the prompt', async (t) => {
  const env = await env0(t)
  env.host.listWorkspaces = async () => WORKSPACES
  await env.bridge.handleUpdate(generalMessage('/new infra'))
  assert.match(lastText(env), /New session in infra/)
  const photo = generalMessage('')
  photo.message.photo = [{ file_id: 'small', file_size: 10 }, { file_id: 'big', file_size: 7 }]
  photo.message.caption = 'what is wrong here?'
  delete photo.message.text
  env.tg.addFile('big', Buffer.from([0xff, 0xd8, 0xff, 0xe0, 1, 2, 3]))
  await env.bridge.handleUpdate(photo)
  await settle(env, 'session-new')
  const input = env.host.calls.find((call) => call.op === 'sendInput')
  assert.equal(input.input.text, 'what is wrong here?')
  assert.equal(input.input.attachments.length, 1)
  const thread = env.store.topic('session-new').threadId
  assert.ok(env.tg.callsOf('sendMessage').some((call) => call.params.message_thread_id === thread && /🖼 photo\nwhat is wrong here\?/.test(call.params.text)))
})

test('General: /new flow can start empty, go back, cancel, and ignores other users\' presses', async (t) => {
  const env = await env0(t)
  env.host.listWorkspaces = async () => WORKSPACES
  env.settings.allowedUserIds = [OWNER, 888]
  await env.bridge.handleUpdate(generalMessage('/new'))
  let buttons = buttonsOf(env.tg.callsOf('sendMessage').at(-1))
  await env.bridge.handleUpdate(press(buttons[0].callback_data, 888, 'cb-other'))
  await settle(env)
  assert.match(env.tg.callsOf('answerCallbackQuery').find((call) => call.params.callback_query_id === 'cb-other').params.text, /Only the person/)

  await env.bridge.handleUpdate(press(buttons[0].callback_data))
  await settle(env)
  buttons = buttonsOf(env.tg.callsOf('editMessageText').at(-1))
  await env.bridge.handleUpdate(press(buttons.find((b) => /Other workspace/.test(b.text)).callback_data))
  await settle(env)
  assert.match(env.tg.callsOf('editMessageText').at(-1).params.text, /Pick a workspace/)
  buttons = buttonsOf(env.tg.callsOf('editMessageText').at(-1))
  await env.bridge.handleUpdate(press(buttons[2].callback_data))
  await settle(env)
  buttons = buttonsOf(env.tg.callsOf('editMessageText').at(-1))
  await env.bridge.handleUpdate(press(buttons.find((b) => /Start without/.test(b.text)).callback_data))
  await settle(env, 'session-new')
  assert.deepEqual(env.host.calls.find((call) => call.op === 'newSession'), { op: 'newSession', path: '/home/u/infra', first: undefined })
  assert.equal(env.host.calls.some((call) => call.op === 'sendInput'), false)
  assert.ok(env.store.topic('session-new'))

  // A fresh flow cancelled with /cancel; later text is not a prompt.
  await env.bridge.handleUpdate(generalMessage('/new'))
  await env.bridge.handleUpdate(generalMessage('/cancel'))
  assert.match(lastText(env), /Cancelled/)
  await env.bridge.handleUpdate(generalMessage('not a prompt'))
  assert.match(lastText(env), /Write inside a session topic/)
  assert.equal(env.host.calls.filter((call) => call.op === 'newSession').length, 1)
})

test('General: /new --full <workspace> <prompt> starts the session with that access', async (t) => {
  const env = await env0(t)
  env.host.listWorkspaces = async () => WORKSPACES
  await env.bridge.handleUpdate(generalMessage('/new --full site deploy it'))
  await settle(env, 'session-new')
  assert.deepEqual(env.host.calls.find((call) => call.op === 'newSession'), { op: 'newSession', path: '/home/u/site', first: undefined, permission: 'danger-full-access' })
  assert.equal(env.host.calls.find((call) => call.op === 'sendInput').input.text, 'deploy it')
  await env.bridge.handleUpdate(generalMessage('/new site --root x'))
  assert.match(lastText(env), /Unknown access/)
})

test('General: the /new picker offers access buttons before the first message', async (t) => {
  const env = await env0(t)
  env.host.listWorkspaces = async () => WORKSPACES
  await env.bridge.handleUpdate(generalMessage('/new infra'))
  const picker = env.tg.callsOf('sendMessage').at(-1)
  assert.match(picker.params.text, /Access: <b>DSH default<\/b>/)
  const ro = buttonsOf(picker).find((b) => /Read Only/.test(b.text))
  await env.bridge.handleUpdate(press(ro.callback_data))
  await settle(env)
  assert.match(env.tg.callsOf('editMessageText').at(-1).params.text, /Access: <b>👁 Read Only<\/b>/)
  await env.bridge.handleUpdate(generalMessage('look around'))
  await settle(env, 'session-new')
  assert.equal(env.host.calls.find((call) => call.op === 'newSession').permission, 'read-only')
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
