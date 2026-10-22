/**
 * Outgoing request scheduler for one Telegram chat (the supergroup).
 *
 * Telegram allows roughly 20 messages per minute in one group, and edits
 * count against the same budget in practice. Every session topic shares that
 * budget, so all chat-bound calls go through one queue that:
 *
 * - enforces a sliding-window budget plus a minimum gap between calls;
 * - serves higher-priority jobs first (approvals before streaming edits);
 * - coalesces jobs by key: a newer job with the same key replaces the queued
 *   one (latest edit of a streaming message wins), so a busy chat degrades to
 *   fewer, larger edits instead of a growing backlog;
 * - honours `retry_after` from 429 responses by pausing and retrying.
 */

export const PRIORITY = Object.freeze({ HIGH: 0, NORMAL: 1, LOW: 2 })

const sleep = (ms, signal) => new Promise((resolve) => {
  if (ms <= 0 || signal?.aborted) return resolve()
  const timer = setTimeout(done, ms)
  function done() {
    signal?.removeEventListener('abort', done)
    clearTimeout(timer)
    resolve()
  }
  signal?.addEventListener('abort', done, { once: true })
})

export class SendQueue {
  /**
   * @param {object} options
   * @param {number} [options.perMinute] - calls allowed in any 60 s window.
   * @param {number} [options.minGapMs] - minimum delay between two calls.
   * @param {number} [options.maxRetries] - retries for 429 and network errors (only
   *   unambiguous ones for jobs pushed with `idempotent: false`).
   * @param {(error: unknown) => void} [options.onError] - report of a job that finally failed.
   * @param {() => number} [options.now]
   */
  constructor({ perMinute = 20, minGapMs = 400, maxRetries = 4, onError, now = Date.now } = {}) {
    this.perMinute = perMinute
    this.minGapMs = minGapMs
    this.maxRetries = maxRetries
    this.onError = onError
    this.now = now
    /** @type {Array<{ seq: number, priority: number, key?: string, run: () => Promise<unknown>, waiters: Array<{resolve: Function, reject: Function}>, attempts: number }>} */
    this.jobs = []
    this.sent = []
    this.seq = 0
    this.pausedUntil = 0
    this.running = false
    this.closed = false
    this.wake = null
    this.abort = new AbortController()
  }

  get size() {
    return this.jobs.length
  }

  /**
   * Queue one call. With a `key`, a queued job with the same key is replaced
   * and both callers receive the result of the newer run.
   * @returns {Promise<unknown>} result of `run()`.
   */
  push(run, { priority = PRIORITY.NORMAL, key, idempotent = true } = {}) {
    if (this.closed) return Promise.reject(new Error('send queue closed'))
    return new Promise((resolve, reject) => {
      if (key !== undefined) {
        const existing = this.jobs.find((job) => job.key === key)
        if (existing) {
          existing.run = run
          existing.priority = Math.min(existing.priority, priority)
          existing.waiters.push({ resolve, reject })
          this.kick()
          return
        }
      }
      this.jobs.push({ seq: this.seq++, priority, key, run, idempotent, waiters: [{ resolve, reject }], attempts: 0 })
      this.kick()
    })
  }

  /** Drop a queued (not yet running) job by key; its waiters resolve undefined. */
  cancel(key) {
    const index = this.jobs.findIndex((job) => job.key === key)
    if (index < 0) return false
    const [job] = this.jobs.splice(index, 1)
    for (const waiter of job.waiters) waiter.resolve(undefined)
    return true
  }

  hasKey(key) {
    return this.jobs.some((job) => job.key === key)
  }

  close() {
    this.closed = true
    this.abort.abort()
    for (const job of this.jobs.splice(0)) for (const waiter of job.waiters) waiter.reject(new Error('send queue closed'))
    this.wake?.()
  }

  kick() {
    if (this.wake) this.wake()
    if (!this.running) {
      this.running = true
      this.loop().finally(() => {
        this.running = false
        if (!this.closed && this.jobs.length > 0) this.kick()
      })
    }
  }

  next() {
    let best = -1
    for (let index = 0; index < this.jobs.length; index += 1) {
      const job = this.jobs[index]
      if (best < 0 || job.priority < this.jobs[best].priority || (job.priority === this.jobs[best].priority && job.seq < this.jobs[best].seq)) best = index
    }
    return best < 0 ? undefined : this.jobs.splice(best, 1)[0]
  }

  delayUntilFree() {
    const now = this.now()
    this.sent = this.sent.filter((at) => now - at < 60000)
    let wait = Math.max(0, this.pausedUntil - now)
    const last = this.sent.at(-1)
    if (last !== undefined) wait = Math.max(wait, last + this.minGapMs - now)
    if (this.sent.length >= this.perMinute) wait = Math.max(wait, this.sent[0] + 60000 - now)
    return wait
  }

  async loop() {
    while (!this.closed && this.jobs.length > 0) {
      const wait = this.delayUntilFree()
      if (wait > 0) {
        await new Promise((resolve) => {
          this.wake = resolve
          sleep(wait, this.abort.signal).then(resolve)
        })
        this.wake = null
        continue
      }
      const job = this.next()
      if (!job) break
      this.sent.push(this.now())
      try {
        const result = await job.run()
        for (const waiter of job.waiters) waiter.resolve(result)
      } catch (error) {
        const retryAfter = error?.retryAfter
        // A non-idempotent call (sendMessage) is retried only when it surely did not land.
        const retryable = error?.isRateLimit || (error?.isNetwork && (job.idempotent !== false || error?.isAmbiguous === false))
        if (retryable && job.attempts < this.maxRetries && !this.closed) {
          job.attempts += 1
          const pause = retryAfter !== undefined ? retryAfter * 1000 + 250 : Math.min(30000, 1000 * 2 ** job.attempts)
          this.pausedUntil = Math.max(this.pausedUntil, this.now() + pause)
          // A newer job with the same key may have been queued meanwhile; it wins.
          if (job.key !== undefined) {
            const newer = this.jobs.find((other) => other.key === job.key)
            if (newer) {
              newer.waiters.push(...job.waiters)
              continue
            }
          }
          this.jobs.push(job)
          continue
        }
        this.onError?.(error)
        for (const waiter of job.waiters) waiter.reject(error)
      }
    }
  }
}
