/**
 * In-process fake of the Telegram Bot API for tests. Never talks to Telegram.
 * Records every call, answers the methods the bridge uses, and lets tests
 * inject updates and one-shot failures.
 */
import { createServer } from 'node:http'

export async function startFakeTelegram({ chatId = -1001234567890 } = {}) {
  const calls = []
  const updates = []
  const failures = new Map()
  const delays = new Map()
  const files = new Map()
  const waiters = []
  const updateWaiters = []
  let updateId = 1000
  let messageId = 1
  let threadId = 100
  const messages = new Map()

  const result = (method, params) => {
    switch (method) {
      case 'getMe':
        return { id: 42, is_bot: true, first_name: 'Fake', username: 'fake_bot' }
      case 'getChat':
        return { id: Number(params.chat_id), type: 'supergroup', title: 'Fake Group', is_forum: true }
      case 'getChatMember':
        return { status: 'administrator', can_manage_topics: true, user: { id: Number(params.user_id) } }
      case 'getUpdates': {
        const offset = Number(params.offset ?? 0)
        while (updates.length > 0 && updates[0].update_id < offset) updates.shift()
        return updates.slice(0, 100)
      }
      case 'getMyCommands':
        return []
      case 'createForumTopic':
        threadId += 1
        return { message_thread_id: threadId, name: params.name, icon_color: 7322096 }
      case 'sendMessage':
      case 'sendDocument':
      case 'sendPhoto': {
        const message = { message_id: messageId++, chat: { id: Number(params.chat_id) }, message_thread_id: params.message_thread_id === undefined ? undefined : Number(params.message_thread_id), text: params.text ?? params.caption, date: Math.floor(Date.now() / 1000) }
        messages.set(message.message_id, message)
        return message
      }
      case 'editMessageText': {
        const message = messages.get(Number(params.message_id))
        if (!message) throw Object.assign(new Error('not found'), { tg: { error_code: 400, description: 'Bad Request: message to edit not found' } })
        if (message.text === params.text) throw Object.assign(new Error('same'), { tg: { error_code: 400, description: 'Bad Request: message is not modified' } })
        message.text = params.text
        return message
      }
      case 'getFile': {
        const path = `files/${params.file_id}`
        return { file_id: params.file_id, file_path: path, file_size: files.get(params.file_id)?.length ?? 0 }
      }
      default:
        return true
    }
  }

  const server = createServer((req, res) => {
    const chunks = []
    req.on('data', (chunk) => chunks.push(chunk))
    req.on('end', async () => {
      const fileMatch = /^\/file\/bot[^/]+\/files\/(.+)$/.exec(req.url)
      if (fileMatch) {
        const data = files.get(decodeURIComponent(fileMatch[1]))
        res.statusCode = data ? 200 : 404
        res.end(data ?? '')
        return
      }
      const match = /^\/bot([^/]+)\/(\w+)$/.exec(req.url)
      if (!match) {
        res.statusCode = 404
        res.end('{}')
        return
      }
      const method = match[2]
      let params = {}
      const raw = Buffer.concat(chunks)
      const type = String(req.headers['content-type'] ?? '')
      if (type.startsWith('application/json')) params = JSON.parse(raw.toString('utf8') || '{}')
      else if (type.startsWith('multipart/form-data')) {
        const form = await new Response(raw, { headers: { 'content-type': type } }).formData()
        for (const [key, value] of form.entries()) params[key] = typeof value === 'string' ? value : { name: value.name, size: value.size, type: value.type }
      }
      if (method === 'getUpdates' && Number(params.timeout) > 0 && !failures.get(method)?.length) {
        // Long poll (shortened): wait for an update or 200 ms.
        const offset = Number(params.offset ?? 0)
        const deadline = Date.now() + 200
        while (Date.now() < deadline && !updates.some((update) => update.update_id >= offset)) {
          await new Promise((resolve) => {
            updateWaiters.push(resolve)
            setTimeout(resolve, 20)
          })
        }
      }
      const delay = delays.get(method)?.shift()
      if (delay) await new Promise((resolve) => setTimeout(resolve, delay))
      calls.push({ method, params })
      for (const waiter of waiters.splice(0)) waiter()
      res.setHeader('content-type', 'application/json')
      const failure = failures.get(method)?.shift()
      if (failure) {
        res.end(JSON.stringify({ ok: false, ...failure }))
        return
      }
      try {
        res.end(JSON.stringify({ ok: true, result: result(method, params) }))
      } catch (error) {
        res.end(JSON.stringify({ ok: false, ...(error.tg ?? { error_code: 500, description: String(error.message) }) }))
      }
    })
  })
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()

  return {
    url: `http://127.0.0.1:${port}`,
    chatId,
    calls,
    messages,
    get lastUpdateId() {
      return updateId
    },
    pushUpdate(update) {
      updateId += 1
      updates.push({ update_id: updateId, ...update })
      for (const waiter of updateWaiters.splice(0)) waiter()
      return updateId
    },
    /** Answer the next call of `method` only after `ms`. */
    slowNext(method, ms) {
      if (!delays.has(method)) delays.set(method, [])
      delays.get(method).push(ms)
    },
    failNext(method, failure) {
      if (!failures.has(method)) failures.set(method, [])
      failures.get(method).push(failure)
    },
    addFile(fileId, buffer) {
      files.set(fileId, buffer)
    },
    callsOf(method) {
      return calls.filter((call) => call.method === method)
    },
    /** Resolve once `predicate(calls)` holds (polls on every new call). */
    async waitFor(predicate, timeoutMs = 3000) {
      const deadline = Date.now() + timeoutMs
      while (!predicate(calls)) {
        if (Date.now() > deadline) throw new Error(`waitFor timed out; calls: ${calls.map((call) => call.method).join(', ')}`)
        await new Promise((resolve) => {
          waiters.push(resolve)
          setTimeout(resolve, 50)
        })
      }
    },
    close: () => new Promise((resolve) => {
      server.closeAllConnections?.()
      server.close(() => resolve())
    }),
  }
}
