import assert from 'node:assert/strict'
import { test } from 'node:test'

/** Minimal React stand-in: records createElement calls and runs function components with stub hooks. */
function fakeReact() {
  const React = {
    createElement: (type, props, ...children) => ({ type, props: props ?? {}, children: children.flat() }),
    Fragment: 'Fragment',
    useState: (initial) => [typeof initial === 'function' ? initial() : initial, () => {}],
    useEffect: () => {},
    useCallback: (fn) => fn,
    useMemo: (fn) => fn(),
    useSyncExternalStore: (_subscribe, getSnapshot) => getSnapshot(),
    memo: (component) => component,
  }
  return React
}

/** Expand function components in an element tree. */
function render(node) {
  if (Array.isArray(node)) return node.map(render)
  if (!node || typeof node !== 'object') return node
  if (typeof node.type === 'function') return render(node.type({ ...node.props, children: node.children }))
  return { ...node, children: node.children.map(render) }
}

function collectText(node, out = []) {
  if (Array.isArray(node)) node.forEach((child) => collectText(child, out))
  else if (typeof node === 'string' || typeof node === 'number') out.push(String(node))
  else if (node && typeof node === 'object') collectText(node.children, out)
  return out
}

async function loadClient() {
  let module
  globalThis.window = { __ModuleLoader__: { load: (mod) => { module = mod } } }
  globalThis.document = { querySelector: () => null, createElement: () => ({ dataset: {}, remove() {} }), head: { appendChild() {} } }
  await import(`../client.js?${Math.random()}`)
  const React = fakeReact()
  return { module, plugin: module.factory((name) => (name === 'react' ? React : {})), React }
}

test('client module registers with the package id and the settings page slot', async () => {
  const { module, plugin } = await loadClient()
  assert.equal(module.id, '@nergous/dsh-tg-bridge')
  assert.deepEqual(plugin.inject, ['slots', 'locale'])
  const registered = []
  const ctx = {
    effect: (fn) => fn(),
    locale: { register: (ns, dicts) => registered.push(['locale', ns, Object.keys(dicts)]) },
    slots: {
      inject: (_name, fn) => fn(),
      register: (meta, component) => registered.push(['slot', meta.name, meta.key ?? meta.id, component]),
    },
    inject: (_names, fn) => fn({
      configForms: {
        get: (id) => {
          assert.equal(id, 'telegram-bridge')
          return { getSnapshot: () => ({ status: 'ready', value: {}, writable: true, revision: 1 }), subscribe: () => () => {}, mutate: async () => true }
        },
      },
      slots: ctx.slots,
    }),
  }
  plugin.apply(ctx)
  assert.deepEqual(registered[0], ['locale', 'telegram-bridge', ['en']])
  const slots = registered.filter((entry) => entry[0] === 'slot').map((entry) => [entry[1], entry[2]])
  assert.deepEqual(slots, [['conversation.composer.dock', 'telegram-bridge-topic'], ['plugins.bundle.config', '@nergous/dsh-tg-bridge']])
})

test('settings page renders all sections and the summary', async () => {
  const { plugin } = await loadClient()
  let Page
  const ctx = {
    effect: (fn) => fn(),
    locale: { register() {} },
    slots: { inject: (_n, fn) => fn(), register: (meta, component) => { if (meta.name === 'plugins.bundle.config') Page = component } },
    inject: (_names, fn) => fn({ configForms: { get: () => ({ getSnapshot: () => ({ status: 'ready', value: { chatId: -100123, verbosity: 'tools', allowedUserIds: [7] }, writable: true, revision: 1 }), subscribe: () => () => {}, mutate: async () => true }) }, slots: ctx.slots }),
  }
  plugin.apply(ctx)
  const form = { getSnapshot: () => ({ status: 'ready', value: { chatId: -100123, verbosity: 'tools', allowedUserIds: [7] }, writable: true, revision: 1 }), subscribe: () => () => {}, mutate: async () => true }
  assert.equal(Page({ view: 'summary', t: (key) => key, form }), 'Every session gets its own Telegram topic')
  const tree = render(Page({ view: 'page', t: (key) => key, form }))
  const text = collectText(tree).join(' ')
  for (const heading of ['Bot', 'Forum group', 'Output', 'Topics', 'Interaction', 'Advanced', 'Bound to group (-100123).', 'Output detail', 'Approvals with buttons', 'Test connection', 'Get bind code']) {
    assert.ok(text.includes(heading), `missing "${heading}"`)
  }
})
