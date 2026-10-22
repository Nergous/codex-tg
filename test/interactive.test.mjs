import assert from 'node:assert/strict'
import { test } from 'node:test'
import { bootPlugin, press, sleep } from './plugin-env.mjs'

/**
 * GUI stand-in for the forwarded waterfall: answers with `guiAnswer` (if
 * any) and, like the gateway, rejects when the request signal is aborted.
 */
function gui(guiAnswer) {
  const seen = { withdrawn: false }
  const inner = (request) => new Promise((resolve, reject) => {
    request.signal?.addEventListener('abort', () => {
      seen.withdrawn = true
      reject(new Error('remote event cancelled'))
    }, { once: true })
    if (guiAnswer) guiAnswer.then(resolve)
  })
  return { inner, seen }
}

function approval(env, extra = {}) {
  return { agent: env.agent, toolName: 'bash', callId: 'call-1', reason: 'escalate sandbox to danger-full-access: push', signal: new AbortController().signal, ...extra }
}

test('approval answered in Telegram wins and withdraws the GUI prompt', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  env.dsh.emit('session/event', env.session, { type: 'tool/call', data: { callId: 'call-1', name: 'bash', arguments: JSON.stringify({ command: 'git push origin main' }) } })
  const { inner, seen } = gui()
  const outcome = env.dsh.waterfall('approval/request', approval(env), inner)
  await press(env, /Allow/)
  assert.equal(await outcome, 'allowed-once')
  assert.equal(seen.withdrawn, true)
  const prompt = env.tg.callsOf('sendMessage').find((call) => /Approval needed/.test(call.params.text))
  assert.match(prompt.params.text, /git push origin main/)
  assert.equal(prompt.params.disable_notification, false)
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'editMessageText' && /Allowed/.test(call.params.text) && /answered in Telegram/.test(call.params.text)))
  assert.ok(env.tg.callsOf('answerCallbackQuery').length >= 1)
})

test('approval denied in Telegram returns rejected', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const { inner } = gui()
  const outcome = env.dsh.waterfall('approval/request', approval(env), inner)
  await press(env, /Deny/)
  assert.equal(await outcome, 'rejected')
})

test('approval answered in the GUI first updates the Telegram prompt and disables its buttons', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  let answer
  const { inner, seen } = gui(new Promise((resolve) => { answer = resolve }))
  const outcome = env.dsh.waterfall('approval/request', approval(env), inner)
  await env.tg.waitFor((calls) => calls.some((call) => /Approval needed/.test(call.params?.text ?? '')))
  answer('rejected')
  assert.equal(await outcome, 'rejected')
  assert.equal(seen.withdrawn, false)
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'editMessageText' && /Rejected/.test(call.params.text) && /answered in DSH/.test(call.params.text)))
  // A late Telegram press is answered as expired.
  await press(env, /Allow/)
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'answerCallbackQuery' && /expired|Already/.test(call.params.text ?? '')))
})

test('a GUI "unavailable" (no client) keeps waiting for Telegram', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const outcome = env.dsh.waterfall('approval/request', approval(env), () => Promise.resolve('unavailable'))
  await sleep(100)
  await press(env, /Allow/)
  assert.equal(await outcome, 'allowed-once')
})

test('a cancelled request settles as cancelled', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const controller = new AbortController()
  const { inner } = gui()
  const outcome = env.dsh.waterfall('approval/request', approval(env, { signal: controller.signal }), inner)
  await env.tg.waitFor((calls) => calls.some((call) => /Approval needed/.test(call.params?.text ?? '')))
  controller.abort()
  assert.equal(await outcome, 'cancelled')
})

test('subagent approvals are routed to the root session topic', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const { agent: child } = env.dsh.addSession('child-1', { origin: 'subagent', parentSession: 'session-a' })
  const { inner } = gui()
  const outcome = env.dsh.waterfall('approval/request', { agent: child, toolName: 'bash', signal: new AbortController().signal }, inner)
  await press(env, /Allow/)
  assert.equal(await outcome, 'allowed-once')
  assert.equal(env.tg.callsOf('createForumTopic').length, 1, 'only the root topic exists')
  const prompt = env.tg.callsOf('sendMessage').find((call) => /Approval needed/.test(call.params.text))
  assert.match(prompt.params.text, /subagent/)
})

test('with approvals disabled the request goes straight to the GUI', async (t) => {
  const env = await bootPlugin({ config: { approvals: false } })
  t.after(env.close)
  const outcome = await env.dsh.waterfall('approval/request', approval(env), () => Promise.resolve('allowed-once'))
  assert.equal(outcome, 'allowed-once')
  assert.equal(env.tg.callsOf('sendMessage').filter((call) => /Approval/.test(call.params.text)).length, 0)
})

test('ask_user_question: single and multi select answered with buttons', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  const request = {
    agent: env.agent,
    signal: new AbortController().signal,
    wait: { callId: 'q-call' },
    questions: [
      { id: 'db', question: 'Which database?', options: [{ label: 'Postgres', description: 'robust' }, { label: 'SQLite' }] },
      { id: 'features', question: 'Which features?', multiSelect: true, options: [{ label: 'Auth' }, { label: 'Billing' }, { label: 'Search' }] },
    ],
  }
  const { inner, seen } = gui()
  const result = env.dsh.waterfall('user-questions/request', request, inner)
  await press(env, /^SQLite$/)
  await press(env, /Auth/)
  await press(env, /Search/)
  await sleep(150)
  await press(env, /Done/)
  const value = await result
  assert.deepEqual(value, { answers: [{ id: 'db', selected: ['SQLite'] }, { id: 'features', selected: ['Auth', 'Search'] }] })
  assert.equal(seen.withdrawn, true)
})

test('ask_user_question answered in the GUI withdraws the Telegram prompt', async (t) => {
  const env = await bootPlugin()
  t.after(env.close)
  let answer
  const { inner } = gui(new Promise((resolve) => { answer = resolve }))
  const result = env.dsh.waterfall('user-questions/request', {
    agent: env.agent,
    signal: new AbortController().signal,
    questions: [{ id: 'q', question: 'Continue?', options: [{ label: 'Yes' }, { label: 'No' }] }],
  }, inner)
  await env.tg.waitFor((calls) => calls.some((call) => /Continue\?/.test(call.params?.text ?? '')))
  await sleep(100)
  answer({ answers: [{ id: 'q', selected: ['Yes'] }] })
  assert.deepEqual(await result, { answers: [{ id: 'q', selected: ['Yes'] }] })
  await env.tg.waitFor((calls) => calls.some((call) => call.method === 'editMessageText' && /Answered in DSH/.test(call.params.text)))
})
