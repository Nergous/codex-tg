import assert from 'node:assert/strict'
import { test } from 'node:test'
import { TelegramError, createBotApi, redactToken } from '../lib/telegram/api.js'
import { PRIORITY, SendQueue } from '../lib/telegram/queue.js'
import { runPoller } from '../lib/telegram/poller.js'
import { startFakeTelegram } from './fake-telegram.mjs'

const TOKEN = '123456789:AAEexampleexampleexampleexampleexa'

test('redactToken removes token-looking strings', () => {
  assert.equal(redactToken(`https://api.telegram.org/bot${TOKEN}/getMe failed`, TOKEN), 'https://api.telegram.org/bot<token>/getMe failed')
  assert.equal(redactToken(`x ${TOKEN} y`), 'x <token> y')
})

test('api call returns result and maps errors with parameters', async (t) => {
  const fake = await startFakeTelegram()
  t.after(() => fake.close())
  const api = createBotApi({ token: () => TOKEN, baseUrl: fake.url })
  const me = await api.call('getMe')
  assert.equal(me.username, 'fake_bot')
  fake.failNext('sendMessage', { error_code: 429, description: 'Too Many Requests: retry after 3', parameters: { retry_after: 3 } })
  await assert.rejects(api.call('sendMessage', { chat_id: 1, text: 'x' }), (error) => {
    assert.ok(error instanceof TelegramError)
    assert.equal(error.isRateLimit, true)
    assert.equal(error.retryAfter, 3)
    assert.doesNotMatch(error.message, /AAE/)
    return true
  })
})

test('api network errors never leak the token', async () => {
  const api = createBotApi({ token: () => TOKEN, baseUrl: 'http://127.0.0.1:9', fetch: async (url) => { throw new Error(`connect failed ${url}`) } })
  await assert.rejects(api.call('getMe'), (error) => {
    assert.equal(error.code, 0)
    assert.doesNotMatch(error.message, /AAE/)
    return true
  })
})

test('api without token fails closed', async () => {
  const api = createBotApi({ token: () => undefined })
  await assert.rejects(api.call('getMe'), /not configured/)
})

test('queue serves higher priority first and coalesces by key', async () => {
  const queue = new SendQueue({ perMinute: 1000, minGapMs: 0 })
  const order = []
  let release
  const gate = new Promise((resolve) => { release = resolve })
  const first = queue.push(async () => { await gate; order.push('first') })
  const low = queue.push(async () => order.push('low-1'), { priority: PRIORITY.LOW, key: 'edit' })
  const low2 = queue.push(async () => { order.push('low-2'); return 'latest' }, { priority: PRIORITY.LOW, key: 'edit' })
  const high = queue.push(async () => order.push('high'), { priority: PRIORITY.HIGH })
  release()
  await Promise.all([first, low, low2, high])
  assert.deepEqual(order, ['first', 'high', 'low-2'])
  assert.equal(await low, 'latest')
  queue.close()
})

test('queue enforces the per-minute window', async () => {
  let now = 0
  const queue = new SendQueue({ perMinute: 2, minGapMs: 0, now: () => now })
  const ran = []
  queue.push(async () => ran.push(1))
  queue.push(async () => ran.push(2))
  const third = queue.push(async () => ran.push(3))
  third.catch(() => {})
  await new Promise((resolve) => setTimeout(resolve, 30))
  assert.deepEqual(ran, [1, 2])
  assert.ok(queue.delayUntilFree() > 59000)
  queue.close()
})

test('queue retries after 429 using retry_after', async () => {
  const queue = new SendQueue({ perMinute: 1000, minGapMs: 0 })
  let attempts = 0
  const started = Date.now()
  const result = await queue.push(async () => {
    attempts += 1
    if (attempts === 1) throw new TelegramError('sendMessage', 429, 'Too Many Requests', { retry_after: 0.1 })
    return 'ok'
  })
  assert.equal(result, 'ok')
  assert.equal(attempts, 2)
  assert.ok(Date.now() - started >= 300)
  queue.close()
})

test('queue rejects non-retryable errors once', async () => {
  const errors = []
  const queue = new SendQueue({ perMinute: 1000, minGapMs: 0, onError: (error) => errors.push(error) })
  await assert.rejects(queue.push(async () => { throw new TelegramError('sendMessage', 400, 'Bad Request: chat not found') }), /chat not found/)
  assert.equal(errors.length, 1)
  queue.close()
})

test('poller delivers updates in order and advances the offset', async (t) => {
  const fake = await startFakeTelegram()
  t.after(() => fake.close())
  const api = createBotApi({ token: () => TOKEN, baseUrl: fake.url })
  fake.pushUpdate({ message: { message_id: 1, text: 'a' } })
  fake.pushUpdate({ message: { message_id: 2, text: 'b' } })
  const controller = new AbortController()
  let offset
  const seen = []
  const done = runPoller({
    api,
    signal: controller.signal,
    timeoutSec: 0,
    getOffset: () => offset,
    setOffset: (value) => { offset = value },
    onUpdate: async (update) => {
      seen.push(update.message.text)
      if (seen.length === 2) controller.abort()
    },
  })
  await done
  assert.deepEqual(seen, ['a', 'b'])
  assert.equal(offset, fake.lastUpdateId + 1)
})

test('poller reports a 409 conflict', async (t) => {
  const fake = await startFakeTelegram()
  t.after(() => fake.close())
  const api = createBotApi({ token: () => TOKEN, baseUrl: fake.url })
  fake.failNext('getUpdates', { error_code: 409, description: 'Conflict: terminated by other getUpdates request' })
  const controller = new AbortController()
  const states = []
  await runPoller({
    api,
    signal: controller.signal,
    timeoutSec: 0,
    getOffset: () => undefined,
    setOffset: () => {},
    onUpdate: async () => {},
    onState: (state) => {
      states.push(state.status)
      controller.abort()
    },
  })
  assert.deepEqual(states, ['conflict'])
})
