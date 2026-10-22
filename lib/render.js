/**
 * Telegram renderings of DSH activity: tool-call lines, subagent status lines,
 * approval and question prompts, and the status card.
 */
import { escapeHtml, expandable, oneLine, truncate } from './format.js'

const ICONS = [
  [/bash|shell|pwsh|terminal|exec|command/i, '⚙️'],
  [/^(read|view|cat)|read_|_read|read_image/i, '📖'],
  [/write|edit|replace|patch|str_replace/i, '✏️'],
  [/grep|search|glob|find/i, '🔎'],
  [/web|fetch|http|browser/i, '🌐'],
  [/subagent|agent|workflow/i, '🤖'],
  [/todo|plan|goal/i, '📋'],
  [/ask_user|question/i, '❓'],
  [/memory/i, '🧠'],
  [/mcp/i, '🔌'],
  [/job/i, '⏱'],
]

export function toolIcon(name) {
  for (const [pattern, icon] of ICONS) if (pattern.test(name)) return icon
  return '🔧'
}

/** Strip harness prefixes such as `custom_` or `mcp__server__` for display. */
export function displayToolName(name) {
  return String(name ?? 'tool').replace(/^custom_/, '').replace(/^mcp__([^_]+)__/, '$1:')
}

const PREVIEW_KEYS = ['command', 'cmd', 'file_path', 'path', 'pattern', 'url', 'query', 'queries', 'description', 'prompt', 'question', 'questions', 'tool', 'name', 'content']

/** Short one-line preview of a tool call's input. */
export function toolPreview(input, max = 90) {
  if (input === null || input === undefined) return ''
  if (typeof input === 'string') return truncate(oneLine(input), max)
  if (typeof input !== 'object') return truncate(String(input), max)
  for (const key of PREVIEW_KEYS) {
    const value = input[key]
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      const first = value[0]
      const text = typeof first === 'string' ? first : first?.question ?? first?.text ?? JSON.stringify(first)
      return truncate(oneLine(`${text}${value.length > 1 ? ` (+${value.length - 1})` : ''}`), max)
    }
    if (typeof value === 'object') continue
    return truncate(oneLine(String(value)), max)
  }
  return ''
}

/**
 * One tool call line in HTML.
 * @param {{ name: string, input?: unknown, state?: 'running' | 'ok' | 'error', error?: string }} call
 */
export function toolLine(call) {
  const status = call.state === 'error' ? ' ❌' : call.state === 'ok' ? '' : ' …'
  const preview = toolPreview(call.input)
  let line = `${toolIcon(call.name)} <b>${escapeHtml(displayToolName(call.name))}</b>`
  if (preview) line += ` <code>${escapeHtml(preview)}</code>`
  line += status
  if (call.state === 'error' && call.error) line += `\n   ${escapeHtml(truncate(oneLine(call.error), 160))}`
  return line
}

/**
 * Collapsed block summarizing a run of tool calls between two assistant
 * texts. Long runs keep the last `max` calls and a count of the earlier ones.
 */
export function toolBlock(calls, { max = 30 } = {}) {
  const shown = calls.slice(-max)
  const hidden = calls.length - shown.length
  const failed = calls.filter((call) => call.state === 'error').length
  const running = calls.filter((call) => call.state !== 'ok' && call.state !== 'error').length
  let head = `🛠 ${calls.length} ${calls.length === 1 ? 'tool' : 'tools'}`
  if (failed) head += ` · ${failed} ❌`
  if (running) head += ' · running…'
  const lines = shown.map(toolLine)
  if (hidden > 0) lines.unshift(`… ${hidden} earlier`)
  return `${head}\n${expandable(lines.join('\n'))}`
}

/**
 * Single line for a subagent in the parent topic.
 * @param {{ name?: string, description?: string, state: 'running' | 'done' | 'failed' | 'cancelled', durationMs?: number }} sub
 */
export function subagentLine(sub) {
  const label = escapeHtml(truncate(oneLine(sub.description || sub.name || 'subagent'), 80))
  const time = sub.durationMs ? ` · ${formatDuration(sub.durationMs)}` : ''
  switch (sub.state) {
    case 'done':
      return `🤖 Subagent <b>${label}</b> — done ✅${time}`
    case 'failed':
      return `🤖 Subagent <b>${label}</b> — failed ❌${time}`
    case 'cancelled':
      return `🤖 Subagent <b>${label}</b> — cancelled ⏹${time}`
    default:
      return `🤖 Subagent <b>${label}</b> — working, waiting for result ⏳`
  }
}

export function formatDuration(ms) {
  const seconds = Math.round(ms / 1000)
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ${seconds % 60}s`
  return `${Math.floor(minutes / 60)}h ${minutes % 60}m`
}

export function formatTokens(count) {
  if (!Number.isFinite(count)) return '?'
  if (count >= 1e6) return `${(count / 1e6).toFixed(1)}M`
  if (count >= 1e3) return `${(count / 1e3).toFixed(1)}k`
  return String(count)
}

/** Approval prompt body. */
export function approvalText(req) {
  const tool = escapeHtml(displayToolName(req.toolName))
  const lines = [`🔐 <b>Approval needed</b> — ${tool}${req.subagent ? ' <i>(subagent)</i>' : ''}`]
  const reason = req.displayReason ?? req.reason
  if (reason) lines.push(escapeHtml(truncate(String(reason), 1500)))
  if (req.preview) lines.push(`<pre>${escapeHtml(truncate(String(req.preview), 2000))}</pre>`)
  return lines.join('\n')
}

export function approvalOutcomeText(req, outcome, via) {
  const label = {
    'allowed-once': '✅ Allowed',
    rejected: '🚫 Rejected',
    cancelled: '⏹ Cancelled',
    unavailable: '⚠️ Unavailable',
  }[outcome] ?? escapeHtml(String(outcome))
  return `${approvalText(req)}\n\n<b>${label}</b>${via ? ` · ${escapeHtml(via)}` : ''}`
}

/** One question of an ask_user_question batch. */
export function questionText(question, index, total) {
  const head = total > 1 ? `❓ <b>Question ${index + 1}/${total}</b>` : '❓ <b>Question</b>'
  const lines = [head]
  if (question.header) lines.push(`<b>${escapeHtml(question.header)}</b>`)
  lines.push(escapeHtml(question.question ?? ''))
  if (question.detail) lines.push(expandable(escapeHtml(truncate(question.detail, 2500))))
  const options = question.options ?? []
  options.forEach((option, i) => {
    lines.push(`${i + 1}. <b>${escapeHtml(option.label)}</b>${option.description ? ` — ${escapeHtml(option.description)}` : ''}`)
  })
  lines.push(question.multi_select || question.multiSelect ? '<i>Tap options, then Done. Or reply with text.</i>' : '<i>Tap an option or reply with text.</i>')
  return lines.join('\n')
}
