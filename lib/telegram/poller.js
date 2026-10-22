/**
 * Long-polling loop for getUpdates.
 *
 * One bot token supports exactly one getUpdates consumer; a second one gets
 * HTTP 409. The poller reports that state instead of fighting over updates.
 */

const sleep = (ms, signal) => new Promise((resolve) => {
  if (signal?.aborted) return resolve()
  const timer = setTimeout(done, ms)
  function done() {
    clearTimeout(timer)
    signal?.removeEventListener('abort', done)
    resolve()
  }
  signal?.addEventListener('abort', done, { once: true })
})

export const ALLOWED_UPDATES = ['message', 'edited_message', 'callback_query', 'my_chat_member']

/**
 * Run until `signal` aborts.
 * @param {object} options
 * @param {ReturnType<import('./api.js').createBotApi>} options.api
 * @param {AbortSignal} options.signal
 * @param {() => number | undefined} options.getOffset - next update_id to request.
 * @param {(offset: number) => void | Promise<void>} options.setOffset - persist after each handled update.
 * @param {(update: object) => Promise<void>} options.onUpdate
 * @param {(state: { status: 'polling' | 'conflict' | 'unauthorized' | 'error', error?: unknown }) => void} [options.onState]
 * @param {number} [options.timeoutSec] - long-poll timeout.
 */
export async function runPoller({ api, signal, getOffset, setOffset, onUpdate, onState, timeoutSec = 30 }) {
  let failures = 0
  let lastStatus
  /** Highest offset reached in this process; survives a failing or late storage. */
  let localOffset = 0
  const report = (status, error) => {
    try {
      if (status !== lastStatus || error) onState?.({ status, error })
    } catch {
      // a reporting bug must not stop polling
    }
    lastStatus = status
  }
  const offset = () => {
    let stored
    try {
      stored = Number(getOffset()) || 0
    } catch {
      stored = 0
    }
    return Math.max(stored, localOffset) || undefined
  }
  while (!signal.aborted) {
    let updates
    try {
      updates = await api.call('getUpdates', { offset: offset(), timeout: timeoutSec, allowed_updates: ALLOWED_UPDATES }, { signal, timeout: (timeoutSec + 15) * 1000 })
      failures = 0
      report('polling')
    } catch (error) {
      if (signal.aborted) return
      failures += 1
      if (error?.code === 401 || error?.code === 404) {
        report('unauthorized', error)
        await sleep(60000, signal)
        continue
      }
      if (error?.code === 409) {
        report('conflict', error)
        await sleep(15000, signal)
        continue
      }
      report('error', error)
      const pause = error?.retryAfter !== undefined ? error.retryAfter * 1000 : Math.min(30000, 1000 * 2 ** Math.min(failures, 5))
      await sleep(pause, signal)
      continue
    }
    for (const update of Array.isArray(updates) ? updates : []) {
      if (signal.aborted) return
      const id = Number(update?.update_id)
      if (!Number.isSafeInteger(id) || id < (offset() ?? 0)) continue // already handled
      localOffset = id + 1
      try {
        await setOffset(localOffset)
      } catch (error) {
        report('error', error)
      }
      try {
        await onUpdate(update)
      } catch (error) {
        report('error', error)
      }
    }
  }
}
