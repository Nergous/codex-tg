import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  TELEGRAM_TEXT_LIMIT,
  escapeHtml,
  inlineToHtml,
  markdownToHtml,
  renderMarkdownMessages,
  splitMarkdown,
  topicName,
  truncate,
} from '../lib/format.js'

test('escapeHtml escapes markup characters', () => {
  assert.equal(escapeHtml('a < b && c > d'), 'a &lt; b &amp;&amp; c &gt; d')
})

test('inline markdown converts emphasis, code and links', () => {
  assert.equal(inlineToHtml('**bold** and *it* and `x<y`'), '<b>bold</b> and <i>it</i> and <code>x&lt;y</code>')
  assert.equal(inlineToHtml('see [docs](https://e.x/a?b=1&c=2)'), 'see <a href="https://e.x/a?b=1&amp;c=2">docs</a>')
  assert.equal(inlineToHtml('~~gone~~'), '<s>gone</s>')
})

test('snake_case identifiers are not italicised', () => {
  assert.equal(inlineToHtml('use my_var_name here'), 'use my_var_name here')
  assert.equal(inlineToHtml('2 * 3 * 4'), '2 * 3 * 4')
})

test('code inside backticks is not formatted', () => {
  assert.equal(inlineToHtml('`**not bold**`'), '<code>**not bold**</code>')
})

test('block markdown: fences, headings, lists, quotes, tables', () => {
  const md = ['# Title', '', '- one', '- [x] done', '> quoted', '', '```js', 'const a = 1 < 2', '```', '| a | b |', '|---|---|'].join('\n')
  const html = markdownToHtml(md)
  assert.match(html, /^<b>Title<\/b>/)
  assert.match(html, /• one/)
  assert.match(html, /• ☑ done/)
  assert.match(html, /<blockquote>quoted<\/blockquote>/)
  assert.match(html, /<pre><code class="language-js">const a = 1 &lt; 2<\/code><\/pre>/)
  assert.match(html, /<pre>\| a \| b \|\n\|---\|---\|<\/pre>/)
})

test('unclosed fence still renders as code', () => {
  assert.equal(markdownToHtml('```\nopen'), '<pre><code>open</code></pre>')
})

test('splitMarkdown keeps chunks under the limit and reopens fences', () => {
  const code = Array.from({ length: 200 }, (_, i) => `line ${i} ${'x'.repeat(30)}`).join('\n')
  const md = `intro\n\n\`\`\`py\n${code}\n\`\`\`\n\noutro`
  const chunks = splitMarkdown(md, 1000)
  assert.ok(chunks.length > 5)
  for (const chunk of chunks) assert.ok(chunk.length <= 1000, `chunk too long: ${chunk.length}`)
  for (const chunk of chunks.slice(1, -1)) assert.match(chunk, /^```py/)
  for (const chunk of chunks.slice(0, -1)) assert.match(chunk, /```$/)
  const html = chunks.map((chunk) => markdownToHtml(chunk))
  for (const piece of html.slice(1, -1)) assert.match(piece, /^<pre><code class="language-py">[\s\S]*<\/code><\/pre>$/)
})

test('splitMarkdown hard-splits one huge line', () => {
  const chunks = splitMarkdown('y'.repeat(9000), 3000)
  assert.ok(chunks.length >= 3)
  assert.equal(chunks.join(''), 'y'.repeat(9000))
})

test('renderMarkdownMessages never exceeds the Telegram limit even with entity growth', () => {
  const md = '<&>'.repeat(5000)
  const messages = renderMarkdownMessages(md)
  assert.ok(messages.length > 1)
  for (const message of messages) assert.ok(message.length <= TELEGRAM_TEXT_LIMIT)
})

test('topicName joins workspace and title within 128 chars', () => {
  assert.equal(topicName('codex-tg', 'Fix login'), 'codex-tg · Fix login')
  assert.equal(topicName('codex-tg', ''), 'codex-tg')
  assert.ok(topicName('w'.repeat(100), 't'.repeat(200)).length <= 128)
  assert.equal(truncate('abcdef', 4), 'abc…')
})
