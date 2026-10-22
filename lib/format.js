/**
 * Markdown to Telegram HTML conversion and message splitting.
 *
 * Agents answer in GitHub-flavoured Markdown; Telegram renders a small HTML
 * subset (b, i, s, u, code, pre, a, blockquote, tg-spoiler). The converter is
 * deliberately forgiving: anything it does not understand is escaped and sent
 * as text. Callers fall back to plain text when Telegram still rejects the
 * markup ("can't parse entities").
 */

/** Telegram's hard limit for one message text, in UTF-16 code units. */
export const TELEGRAM_TEXT_LIMIT = 4096
/** Default Markdown chunk size; leaves room for HTML tag and entity growth. */
export const DEFAULT_CHUNK = 3400
/** Telegram's limit for a forum topic name. */
export const TOPIC_NAME_LIMIT = 128

export function escapeHtml(text) {
  return String(text).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

const escapeAttr = (text) => escapeHtml(text).replace(/"/g, '&quot;')

/** Shorten to `max` code points with an ellipsis. */
export function truncate(text, max) {
  const chars = Array.from(String(text ?? ''))
  if (chars.length <= max) return chars.join('')
  return chars.slice(0, Math.max(0, max - 1)).join('') + '…'
}

/** Collapse whitespace to single spaces (one-line previews). */
export function oneLine(text) {
  return String(text ?? '').replace(/\s+/g, ' ').trim()
}

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([^\s`]*)\s*$/

/** Inline Markdown (one line or paragraph, no block syntax) to HTML. */
export function inlineToHtml(text) {
  const parts = String(text).split(/(`[^`\n]+`)/)
  let out = ''
  for (const part of parts) {
    if (part.length > 1 && part.startsWith('`') && part.endsWith('`')) {
      out += `<code>${escapeHtml(part.slice(1, -1))}</code>`
      continue
    }
    let s = escapeHtml(part)
    // Links first so their text can still carry emphasis.
    s = s.replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g, (_, label, url) => `<a href="${escapeAttr(url.replace(/&amp;/g, '&'))}">${label}</a>`)
    s = s.replace(/\*\*(?=\S)([^*\n]*?\S)\*\*/g, '<b>$1</b>')
    s = s.replace(/(^|[^\w])__(?=\S)([^_\n]*?\S)__(?![\w])/g, '$1<b>$2</b>')
    s = s.replace(/~~(?=\S)([^~\n]*?\S)~~/g, '<s>$1</s>')
    s = s.replace(/(^|[^\w*])\*(?=[^\s*])([^*\n]*?[^\s*])\*(?![\w*])/g, '$1<i>$2</i>')
    s = s.replace(/(^|[^\w_])_(?=[^\s_])([^_\n]*?[^\s_])_(?![\w_])/g, '$1<i>$2</i>')
    s = s.replace(/\|\|(?=\S)([^|\n]*?\S)\|\|/g, '<tg-spoiler>$1</tg-spoiler>')
    out += s
  }
  return out
}

/** Block-level Markdown to Telegram HTML. */
export function markdownToHtml(markdown) {
  const lines = String(markdown ?? '').replace(/\r\n?/g, '\n').split('\n')
  const out = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    const fence = FENCE.exec(line)
    if (fence) {
      const marker = fence[1]
      const lang = fence[2]
      const body = []
      i += 1
      while (i < lines.length && !(lines[i].trim().startsWith(marker[0].repeat(marker.length)) && lines[i].trim().replace(/[`~]/g, '') === '')) {
        body.push(lines[i])
        i += 1
      }
      i += 1 // closing fence (or end of input)
      const cls = lang ? ` class="language-${escapeAttr(lang)}"` : ''
      out.push(`<pre><code${cls}>${escapeHtml(body.join('\n'))}</code></pre>`)
      continue
    }
    if (/^\s*\|.*\|\s*$/.test(line)) {
      const table = []
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) {
        table.push(lines[i].trim())
        i += 1
      }
      out.push(`<pre>${escapeHtml(table.join('\n'))}</pre>`)
      continue
    }
    if (/^\s{0,3}>/.test(line)) {
      const quote = []
      while (i < lines.length && /^\s{0,3}>/.test(lines[i])) {
        quote.push(inlineToHtml(lines[i].replace(/^\s{0,3}>\s?/, '')))
        i += 1
      }
      out.push(`<blockquote>${quote.join('\n')}</blockquote>`)
      continue
    }
    const heading = /^\s{0,3}#{1,6}\s+(.*?)\s*#*\s*$/.exec(line)
    if (heading) {
      out.push(`<b>${inlineToHtml(heading[1])}</b>`)
      i += 1
      continue
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) {
      out.push('──────────')
      i += 1
      continue
    }
    const bullet = /^(\s*)[-*+]\s+(\[[ xX]\]\s+)?(.*)$/.exec(line)
    if (bullet) {
      const indent = ' '.repeat(Math.min(bullet[1].length, 8))
      const box = bullet[2] ? (/x/i.test(bullet[2]) ? '☑ ' : '☐ ') : ''
      out.push(`${indent}• ${box}${inlineToHtml(bullet[3])}`)
      i += 1
      continue
    }
    out.push(inlineToHtml(line))
    i += 1
  }
  return out.join('\n').replace(/\n{3,}/g, '\n\n').trim()
}

/** Strip Markdown to readable plain text (fallback when HTML is rejected). */
export function markdownToPlain(markdown) {
  return String(markdown ?? '')
    .replace(/\r\n?/g, '\n')
    .replace(/^\s{0,3}(`{3,}|~{3,}).*$/gm, '')
    .replace(/\*\*([^*\n]+)\*\*/g, '$1')
    .replace(/\[([^\]\n]+)\]\((https?:\/\/[^\s)]+)\)/g, '$1 ($2)')
    .trim()
}

function hardSplit(line, limit) {
  const pieces = []
  const chars = Array.from(line)
  for (let at = 0; at < chars.length; at += limit) pieces.push(chars.slice(at, at + limit).join(''))
  return pieces
}

/**
 * Split Markdown into chunks no longer than `limit` characters. Splits happen
 * on line boundaries; an open code fence is closed at the end of a chunk and
 * reopened (same info string) at the start of the next one, so every chunk
 * renders on its own.
 */
export function splitMarkdown(markdown, limit = DEFAULT_CHUNK) {
  const text = String(markdown ?? '').replace(/\r\n?/g, '\n')
  if (text.length <= limit) return text.trim() === '' ? [] : [text]
  const chunks = []
  let current = []
  let size = 0
  let fence = null // { open: '```lang', close: '```' }
  const flush = () => {
    if (current.length === 0) return
    if (fence) current.push(fence.close)
    const chunk = current.join('\n')
    if (chunk.trim() !== '') chunks.push(chunk)
    current = fence ? [fence.open] : []
    size = fence ? fence.open.length + 1 : 0
  }
  const reserve = () => (fence ? fence.close.length + 1 : 0)
  for (const rawLine of text.split('\n')) {
    const pieces = rawLine.length > limit - 16 ? hardSplit(rawLine, limit - 16) : [rawLine]
    for (const line of pieces) {
      if (size + line.length + 1 + reserve() > limit && current.length > (fence ? 1 : 0)) flush()
      current.push(line)
      size += line.length + 1
      const match = FENCE.exec(line)
      if (match) {
        if (fence === null) fence = { open: line.trim(), close: match[1][0].repeat(match[1].length) }
        else if (line.trim().replace(/[`~]/g, '') === '') fence = null
      }
    }
  }
  if (current.length > (fence ? 1 : 0)) {
    const chunk = current.join('\n')
    if (chunk.trim() !== '') chunks.push(chunk)
  }
  return chunks
}

/**
 * Render Markdown into an array of Telegram HTML messages, each within the
 * Telegram limit. Chunks whose HTML grew past the limit are split again.
 */
export function renderMarkdownMessages(markdown, limit = DEFAULT_CHUNK) {
  const out = []
  for (const chunk of splitMarkdown(markdown, limit)) {
    const html = markdownToHtml(chunk)
    if (html.length <= TELEGRAM_TEXT_LIMIT) {
      if (html !== '') out.push(html)
      continue
    }
    if (limit > 600) out.push(...renderMarkdownMessages(chunk, Math.floor(limit / 2)))
    else for (const piece of hardSplit(chunk, 1000)) out.push(escapeHtml(piece))
  }
  return out
}

/** Collapsed-by-default quote block (Telegram "expandable blockquote"). */
export function expandable(html) {
  return `<blockquote expandable>${html}</blockquote>`
}

/** Topic name `workspace · title`, within Telegram's 128-character limit. */
export function topicName(workspace, title) {
  const ws = oneLine(workspace) || 'workspace'
  const t = oneLine(title)
  if (!t) return truncate(ws, TOPIC_NAME_LIMIT)
  const head = truncate(ws, 40)
  return truncate(`${head} · ${t}`, TOPIC_NAME_LIMIT)
}
