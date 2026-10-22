/**
 * Minimal Telegram Bot API client on top of global fetch.
 *
 * The bot token is part of every request URL, so no error produced here may
 * contain the URL: network failures are rewrapped with the method name only.
 */

export class TelegramError extends Error {
  /**
   * @param {string} method - Bot API method name.
   * @param {number} code - Telegram `error_code` or HTTP status (0 = network).
   * @param {string} description - Telegram description, never containing the token.
   * @param {object} [parameters] - Telegram ResponseParameters (retry_after, migrate_to_chat_id).
   */
  constructor(method, code, description, parameters = {}) {
    super(`telegram ${method}: ${code === 0 ? '' : `${code} `}${description}`)
    this.name = 'TelegramError'
    this.method = method
    this.code = code
    this.description = description
    this.parameters = parameters ?? {}
  }

  get retryAfter() {
    const value = Number(this.parameters?.retry_after)
    return Number.isFinite(value) && value > 0 ? value : undefined
  }

  get isRateLimit() {
    return this.code === 429
  }

  get isNetwork() {
    return this.code === 0 || this.code >= 500
  }

  /**
   * The request may have reached Telegram (timeout, reset, 5xx): retrying a
   * non-idempotent call such as sendMessage could duplicate it.
   */
  get isAmbiguous() {
    if (this.code >= 500) return true
    if (this.code !== 0) return false
    return !/ECONNREFUSED|ENOTFOUND|EAI_AGAIN|ENETUNREACH|EHOSTUNREACH|token is not configured/.test(this.description)
  }

  /** "Bad Request: message is not modified" — an edit with identical content. */
  get isNotModified() {
    return this.code === 400 && /message is not modified/i.test(this.description)
  }

  /** HTML markup the server could not parse. */
  get isParseError() {
    return this.code === 400 && /can't parse entities|unsupported start tag|can't find end tag|unexpected end tag/i.test(this.description)
  }

  /** Target message, thread, or topic no longer exists. */
  get isGone() {
    return this.code === 400 && /message to edit not found|message thread not found|topic_deleted|TOPIC_DELETED|thread not found|message to delete not found|MESSAGE_ID_INVALID/i.test(this.description)
  }

  get isTopicClosed() {
    return this.code === 400 && /TOPIC_CLOSED/i.test(this.description)
  }
}

/** Remove anything that looks like a bot token from a string. */
export function redactToken(text, token) {
  let out = String(text ?? '')
  if (token) out = out.split(token).join('<token>')
  return out.replace(/\b\d{6,}:[A-Za-z0-9_-]{30,}\b/g, '<token>')
}

const isBlobLike = (value) => value !== null && typeof value === 'object' && typeof value.arrayBuffer === 'function' && typeof value.size === 'number'

/**
 * @param {object} options
 * @param {() => string | undefined} options.token - current bot token (read lazily, so a token change applies without restart).
 * @param {string | (() => string)} [options.baseUrl] - Bot API root (tests point this at a fake server).
 * @param {typeof fetch} [options.fetch]
 * @param {number} [options.timeoutMs] - per-request timeout for non-polling calls.
 */
export function createBotApi({ token, baseUrl = 'https://api.telegram.org', fetch: fetchImpl = globalThis.fetch, timeoutMs = 30000 }) {
  // A function lets a settings change apply without rebuilding the client.
  const rootUrl = () => String((typeof baseUrl === 'function' ? baseUrl() : baseUrl) || 'https://api.telegram.org').replace(/\/+$/, '')

  async function call(method, params = {}, { signal, timeout = timeoutMs } = {}) {
    const secret = token()
    if (!secret) throw new TelegramError(method, 401, 'bot token is not configured')
    const hasFile = Object.values(params).some(isBlobLike)
    let body
    const headers = {}
    if (hasFile) {
      body = new FormData()
      for (const [key, value] of Object.entries(params)) {
        if (value === undefined || value === null) continue
        if (isBlobLike(value)) body.append(key, value, value.name ?? key)
        else body.append(key, typeof value === 'object' ? JSON.stringify(value) : String(value))
      }
    } else {
      const clean = {}
      for (const [key, value] of Object.entries(params)) if (value !== undefined && value !== null) clean[key] = value
      body = JSON.stringify(clean)
      headers['content-type'] = 'application/json'
    }
    const timer = AbortSignal.timeout(timeout)
    const combined = signal ? AbortSignal.any([signal, timer]) : timer
    let response
    try {
      response = await fetchImpl(`${rootUrl()}/bot${secret}/${method}`, { method: 'POST', body, headers, signal: combined })
    } catch (error) {
      if (signal?.aborted) throw signal.reason ?? error
      const reason = timer.aborted ? 'request timed out' : `network error: ${redactToken(error?.cause?.code ?? error?.message ?? error, secret)}`
      throw new TelegramError(method, 0, reason)
    }
    let payload
    try {
      payload = await response.json()
    } catch {
      throw new TelegramError(method, response.status || 0, `invalid response (HTTP ${response.status})`)
    }
    if (payload?.ok === true) return payload.result
    throw new TelegramError(method, Number(payload?.error_code ?? response.status) || 0, redactToken(payload?.description ?? `HTTP ${response.status}`, secret), payload?.parameters)
  }

  /** Download a file by `file_path` from getFile. */
  async function download(filePath, { signal, maxBytes = 20 * 1024 * 1024 } = {}) {
    const secret = token()
    if (!secret) throw new TelegramError('getFile', 401, 'bot token is not configured')
    let response
    try {
      response = await fetchImpl(`${rootUrl()}/file/bot${secret}/${filePath}`, { signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(120000)]) : AbortSignal.timeout(120000) })
    } catch (error) {
      throw new TelegramError('download', 0, `network error: ${redactToken(error?.message ?? error, secret)}`)
    }
    if (!response.ok) throw new TelegramError('download', response.status, `HTTP ${response.status}`)
    const buffer = Buffer.from(await response.arrayBuffer())
    if (buffer.length > maxBytes) throw new TelegramError('download', 413, `file larger than ${maxBytes} bytes`)
    return buffer
  }

  return { call, download }
}
