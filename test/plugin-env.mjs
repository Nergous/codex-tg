/** Boot the real plugin (index.js apply) against fake DSH services and a fake Telegram. */
import { apply } from '../index.js'
import { createFakeDsh } from './fake-dsh.mjs'
import { startFakeTelegram } from './fake-telegram.mjs'
import { CHAT, OWNER, TOKEN, sleep } from './harness.mjs'

export async function bootPlugin({ config = {}, token = TOKEN, waitForPoll = true } = {}) {
  const tg = await startFakeTelegram({ chatId: CHAT })
  const dsh = createFakeDsh({ token })
  const { session, agent } = dsh.addSession('session-a')
  apply(dsh.ctx, {
    chatId: CHAT,
    allowedUserIds: [OWNER],
    apiBaseUrl: tg.url,
    streamIntervalMs: 100,
    typingIndicator: false,
    perMinute: 60,
    ...config,
  })
  // let storage open and the poller start
  if (waitForPoll) await tg.waitFor((calls) => calls.some((call) => call.method === 'getUpdates'), 5000)
  const close = async () => {
    await dsh.dispose()
    await tg.close()
  }
  return { tg, dsh, session, agent, close }
}

/** Press an inline button whose callback_data matches `pattern` on the latest message having it. */
export async function press(env, pattern) {
  let target
  await env.tg.waitFor((calls) => {
    for (const call of [...calls].reverse()) {
      const markup = call.params?.reply_markup
      const keyboard = typeof markup === 'string' ? JSON.parse(markup) : markup
      const button = keyboard?.inline_keyboard?.flat().find((b) => pattern.test(b.text) || pattern.test(b.callback_data))
      if (button && call.method === 'sendMessage') {
        const sent = [...env.tg.messages.values()].find((m) => m.text === call.params.text)
        target = { data: button.callback_data, messageId: sent?.message_id }
        return true
      }
    }
    return false
  }, 5000)
  env.tg.pushUpdate({
    callback_query: {
      id: `cb-${Math.random()}`,
      from: { id: OWNER, is_bot: false },
      message: { message_id: target.messageId, chat: { id: CHAT } },
      data: target.data,
    },
  })
  return target
}

export { sleep }
