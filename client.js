window.__ModuleLoader__.load({
  id: '@nergous/dsh-tg-bridge',
  factory(require) {
    const React = require('react')
    const h = React.createElement

    const PKG = '@nergous/dsh-tg-bridge'
    const ENTRY = 'telegram-bridge'
    const NS = 'telegram-bridge'
    const CSS_ID = `${PKG}/style`
    const ROUTES = {
      status: 'telegram-bridge/status',
      test: 'telegram-bridge/test',
      token: 'telegram-bridge/token',
      bind: 'telegram-bridge/bind',
      unbind: 'telegram-bridge/unbind',
      session: 'telegram-bridge/session',
    }

    const CSS = [
      '.tgb-page{display:flex;flex-direction:column;gap:18px;font-size:13px;line-height:20px;color:var(--dsw-alias-label-secondary)}',
      '.tgb-section{display:flex;flex-direction:column;gap:10px}',
      '.tgb-h{color:var(--dsw-alias-label-primary);font-weight:500;font-size:14px;margin:0}',
      '.tgb-desc{color:var(--dsw-alias-label-tertiary);margin:0}',
      '.tgb-row{display:flex;align-items:center;gap:10px;flex-wrap:wrap}',
      '.tgb-field{display:grid;grid-template-columns:minmax(160px,220px) 1fr;gap:6px 14px;align-items:center}',
      '.tgb-field label{color:var(--dsw-alias-label-primary)}',
      '.tgb-hint{grid-column:2;color:var(--dsw-alias-label-tertiary);font-size:12px;line-height:16px;margin-top:-4px}',
      '.tgb-input,.tgb-select{font:inherit;font-size:13px;line-height:20px;padding:4px 8px;border-radius:var(--dsw-radius-sm);',
      'border:1px solid var(--dsw-alias-border-l2,var(--dsw-alias-border-l1));background:var(--dsw-alias-bg-l1,transparent);color:var(--dsw-alias-label-primary);outline:none;min-width:0}',
      '.tgb-input:focus,.tgb-select:focus{border-color:var(--dsw-alias-accent-primary,var(--dsw-alias-border-l3))}',
      '.tgb-btn{font:inherit;font-size:13px;line-height:20px;border:none;cursor:pointer;border-radius:var(--dsw-radius-sm);padding:4px 12px;',
      'background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-primary);flex:none}',
      '.tgb-btn:hover{background:var(--dsw-alias-interactive-bg-pressed,var(--dsw-alias-interactive-bg-hover))}',
      '.tgb-btn:disabled{opacity:.5;cursor:default}',
      '.tgb-btn[data-tone=primary]{background:var(--dsw-alias-accent-primary,#2aabee);color:var(--dsw-static-neutral-bluish-00,#fff)}',
      '.tgb-status{display:flex;flex-direction:column;gap:2px;padding:8px 10px;border-radius:var(--dsw-radius-sm);background:var(--dsw-alias-bg-l1,transparent);border:1px solid var(--dsw-alias-border-l1)}',
      '.tgb-ok{color:var(--dsw-alias-state-success-primary,#2e9d5b)}',
      '.tgb-warn{color:var(--dsw-alias-state-warn-primary)}',
      '.tgb-err{color:var(--dsw-alias-state-error-primary)}',
      '.tgb-code{font-family:var(--dsh-font-mono,monospace);font-size:13px;padding:2px 6px;border-radius:4px;background:var(--dsw-alias-interactive-bg-hover);user-select:all}',
      '.tgb-steps{margin:0;padding-left:20px;display:flex;flex-direction:column;gap:4px}',
      '.tgb-pill{flex:none;display:inline-flex;align-items:center;gap:5px;padding:1px 8px;border-radius:var(--dsw-radius-sm);border:none;background:0 0;',
      'color:var(--dsw-alias-label-tertiary);font:inherit;font-size:var(--dsh-content-font-size-secondary,13px);line-height:20px;cursor:pointer;text-decoration:none;white-space:nowrap}',
      '.tgb-pill:hover{background:var(--dsw-alias-interactive-bg-hover);color:var(--dsw-alias-label-secondary)}',
      '.tgb-pill[data-muted=true]{opacity:.6}',
    ].join('')

    const en = {
      summary: 'Every session gets its own Telegram topic',
      'conn.title': 'Bot',
      'conn.desc': 'Create a bot with @BotFather and paste its token. It is stored in DSH credentials, never in config files.',
      'conn.token': 'Bot token',
      'conn.tokenSet': 'Token is stored ({source}).',
      'conn.tokenMissing': 'No token yet.',
      'conn.save': 'Save token',
      'conn.test': 'Test connection',
      'conn.bot': 'Bot: @{username}',
      'conn.poller': 'Polling: {state}',
      'group.title': 'Forum group',
      'group.desc': 'One supergroup with Topics enabled. The bot must be an administrator with "Manage topics" (and "Pin messages" for the status card).',
      'group.bound': 'Bound to {title} ({id}).',
      'group.unbound': 'No group bound yet.',
      'group.bind': 'Get bind code',
      'group.unbind': 'Unbind',
      'group.step1': 'Create a group, open its settings and enable Topics.',
      'group.step2': 'Add the bot and make it an administrator with Manage topics.',
      'group.step3': 'Send this in the group (valid 10 minutes):',
      'group.users': 'Allowed Telegram user ids',
      'group.usersHint': 'Comma separated. /bind adds you automatically. Messages from anyone else are ignored.',
      'out.title': 'Output',
      'top.title': 'Topics',
      'int.title': 'Interaction',
      'adv.title': 'Advanced',
      'f.enabled': 'Bridge enabled',
      'f.verbosity': 'Output detail',
      'f.verbosity.full': 'Everything, tools expanded',
      'f.verbosity.tools': 'Tools collapsed (default)',
      'f.verbosity.final': 'Final answers only',
      'f.streaming': 'Stream answers live',
      'f.streamIntervalMs': 'Stream edit interval, ms',
      'f.notifyOnTurnEnd': 'Notify when a turn ends',
      'f.typingIndicator': 'Show "typing…" while working',
      'f.mirrorGuiMessages': 'Mirror my DSH messages',
      'f.pinStatus': 'Pinned status card per topic',
      'f.createTopicOn': 'Create topic',
      'f.createTopicOn.first-message': 'On first message',
      'f.createTopicOn.agent-created': 'As soon as a session opens',
      'f.createTopicOn.never': 'Never automatically',
      'f.onSessionArchived': 'When a session is archived',
      'f.onSessionArchived.close': 'Close the topic',
      'f.onSessionArchived.delete': 'Delete the topic',
      'f.onSessionArchived.keep': 'Keep it open',
      'f.approvals': 'Approvals with buttons',
      'f.questions': 'Agent questions with buttons',
      'f.voice': 'Transcribe voice messages',
      'f.transcribeCommand': 'Transcription command',
      'f.transcribeCommand.hint': 'Optional, e.g. whisper-cli -l ru -nt -f {file}. Without it DSH speech-to-text is used when installed.',
      'f.transcribeLanguage': 'Speech language (DSH STT)',
      'f.ffmpegPath': 'ffmpeg path',
      'f.sendPresentedFiles': 'Send files the agent presents',
      'f.steerPrefix': 'Steer prefix',
      'f.steerPrefix.hint': 'A message starting with this steers the running turn instead of queueing.',
      'f.maxDownloadMb': 'Max download, MB',
      'f.perMinute': 'Messages per minute',
      'f.apiBaseUrl': 'Bot API URL',
      saved: 'Saved',
      saveFailed: 'Could not save',
      loading: 'Loading…',
      unavailable: 'Settings are unavailable.',
      'pill.open': 'Open this session in Telegram',
      'pill.none': 'No Telegram topic yet',
    }

    const interpolate = (text, vars) => (vars ? text.replace(/\{(\w+)\}/g, (m, k) => (vars[k] === undefined ? m : String(vars[k]))) : text)
    function translator(t) {
      return (key, vars) => {
        let value
        try {
          value = typeof t === 'function' ? t(key, vars) : undefined
        } catch {
          value = undefined
        }
        return typeof value === 'string' && value !== '' && value !== key ? value : interpolate(en[key] ?? key, vars)
      }
    }

    async function getJson(url) {
      try {
        const response = await fetch(url, { cache: 'no-store', headers: { accept: 'application/json' } })
        return response.ok ? await response.json() : { ok: false, message: `HTTP ${response.status}` }
      } catch (error) {
        return { ok: false, message: String(error?.message ?? error) }
      }
    }
    async function postJson(url, body) {
      try {
        const response = await fetch(url, { method: 'POST', cache: 'no-store', headers: { 'content-type': 'application/json', accept: 'application/json' }, body: JSON.stringify(body ?? {}) })
        return response.ok ? await response.json() : { ok: false, message: `HTTP ${response.status}` }
      } catch (error) {
        return { ok: false, message: String(error?.message ?? error) }
      }
    }

    function useStatus() {
      const [status, setStatus] = React.useState(null)
      const reload = React.useCallback(async () => setStatus(await getJson(ROUTES.status)), [])
      React.useEffect(() => {
        let live = true
        let timer
        const loop = async () => {
          const next = await getJson(ROUTES.status)
          if (!live) return
          setStatus(next)
          timer = setTimeout(loop, next?.bindCode ? 2000 : 6000)
        }
        loop()
        return () => {
          live = false
          clearTimeout(timer)
        }
      }, [])
      return [status, reload]
    }

    const FIELDS = {
      out: [
        { key: 'verbosity', type: 'select', options: ['tools', 'full', 'final'] },
        { key: 'streaming', type: 'bool' },
        { key: 'notifyOnTurnEnd', type: 'bool' },
        { key: 'typingIndicator', type: 'bool' },
        { key: 'mirrorGuiMessages', type: 'bool' },
        { key: 'pinStatus', type: 'bool' },
      ],
      top: [
        { key: 'createTopicOn', type: 'select', options: ['first-message', 'agent-created', 'never'] },
        { key: 'onSessionArchived', type: 'select', options: ['close', 'delete', 'keep'] },
      ],
      int: [
        { key: 'approvals', type: 'bool' },
        { key: 'questions', type: 'bool' },
        { key: 'sendPresentedFiles', type: 'bool' },
        { key: 'steerPrefix', type: 'text', hint: true },
        { key: 'voice', type: 'bool' },
        { key: 'transcribeCommand', type: 'text', hint: true },
        { key: 'transcribeLanguage', type: 'text' },
      ],
      adv: [
        { key: 'enabled', type: 'bool' },
        { key: 'streamIntervalMs', type: 'number' },
        { key: 'perMinute', type: 'number' },
        { key: 'maxDownloadMb', type: 'number' },
        { key: 'ffmpegPath', type: 'text' },
        { key: 'apiBaseUrl', type: 'text' },
      ],
    }

    function Field({ field, value, tr, disabled, onSave }) {
      const [draft, setDraft] = React.useState(null)
      const id = `tgb-${field.key}`
      let control
      if (field.type === 'bool') {
        control = h('input', { id, type: 'checkbox', checked: value === true, disabled, onChange: (event) => onSave(field.key, event.target.checked) })
      } else if (field.type === 'select') {
        control = h('select', { id, className: 'tgb-select', value: value ?? '', disabled, onChange: (event) => onSave(field.key, event.target.value) },
          field.options.map((option) => h('option', { key: option, value: option }, tr(`f.${field.key}.${option}`))))
      } else {
        const shown = draft ?? (value === undefined || value === null ? '' : String(value))
        const commit = () => {
          if (draft === null) return
          const next = field.type === 'number' ? Number(draft) : draft
          if (field.type === 'number' && !Number.isFinite(next)) return
          onSave(field.key, next).then(() => setDraft(null))
        }
        control = h('input', {
          id,
          className: 'tgb-input',
          type: field.type === 'number' ? 'number' : 'text',
          value: shown,
          disabled,
          onChange: (event) => setDraft(event.target.value),
          onBlur: commit,
          onKeyDown: (event) => { if (event.key === 'Enter') commit() },
        })
      }
      return h(React.Fragment, null,
        h('label', { htmlFor: id }, tr(`f.${field.key}`)),
        control,
        field.hint ? h('div', { className: 'tgb-hint' }, tr(`f.${field.key}.hint`)) : null)
    }

    function Section({ title, desc, children }) {
      return h('section', { className: 'tgb-section' }, h('h3', { className: 'tgb-h' }, title), desc ? h('p', { className: 'tgb-desc' }, desc) : null, children)
    }

    function SettingsPage({ view, t, form }) {
      const tr = React.useMemo(() => translator(t), [t])
      const snap = React.useSyncExternalStore(form.subscribe, form.getSnapshot)
      const [status, reload] = useStatus()
      const [token, setToken] = React.useState('')
      const [result, setResult] = React.useState(null)
      const [busy, setBusy] = React.useState(false)
      const [users, setUsers] = React.useState(null)
      if (view === 'summary') return tr('summary')
      if (snap.status === 'loading') return h('p', { role: 'status' }, tr('loading'))
      if (snap.status !== 'ready') return h('p', { role: 'status' }, tr('unavailable'))
      const config = { ...(status?.settings ?? {}), ...(snap.value ?? {}) }
      const disabled = !snap.writable
      const save = async (key, value) => {
        const ok = await form.mutate([{ op: 'set', path: [key], value }], form.getSnapshot().revision)
        setResult(ok ? { ok: true, message: tr('saved') } : { ok: false, message: tr('saveFailed') })
        setTimeout(reload, 400)
        return ok
      }
      const run = async (fn) => {
        setBusy(true)
        try {
          setResult(await fn())
        } finally {
          setBusy(false)
          reload()
        }
      }
      const fields = (group) => h('div', { className: 'tgb-field' }, FIELDS[group].map((field) => h(Field, { key: field.key, field, value: config[field.key], tr, disabled, onSave: save })))
      const chatId = Number(config.chatId) || 0
      const usersText = users ?? (Array.isArray(config.allowedUserIds) ? config.allowedUserIds.join(', ') : '')

      return h('div', { className: 'tgb-page', 'data-telegram-bridge-settings': true },
        h(Section, { title: tr('conn.title'), desc: tr('conn.desc') },
          h('div', { className: 'tgb-status' },
            h('span', { className: status?.token?.configured ? 'tgb-ok' : 'tgb-warn' }, status?.token?.configured ? tr('conn.tokenSet', { source: status.token.source ?? 'credentials' }) : tr('conn.tokenMissing')),
            status?.bot ? h('span', null, tr('conn.bot', { username: status.bot.username })) : null,
            status ? h('span', { className: status.poller === 'polling' ? 'tgb-ok' : 'tgb-warn' }, tr('conn.poller', { state: status.poller + (status.pollerError ? ` — ${status.pollerError}` : '') })) : null,
            status?.lastError ? h('span', { className: 'tgb-err' }, status.lastError) : null),
          h('div', { className: 'tgb-row' },
            h('input', { className: 'tgb-input', type: 'password', autoComplete: 'new-password', placeholder: '123456789:AA…', value: token, style: { flex: 1 }, onChange: (event) => setToken(event.target.value.trim()) }),
            h('button', { type: 'button', className: 'tgb-btn', 'data-tone': 'primary', disabled: !token || busy, onClick: () => run(async () => { const r = await postJson(ROUTES.token, { token }); if (r.ok !== false || r.code !== 'format') setToken(''); return r }) }, tr('conn.save')),
            h('button', { type: 'button', className: 'tgb-btn', disabled: busy, onClick: () => run(() => postJson(ROUTES.test)) }, tr('conn.test'))),
          result ? h('p', { className: result.ok === false ? 'tgb-err' : 'tgb-ok', role: 'status' }, result.message ?? (result.ok ? `OK${result.bot ? ` — @${result.bot.username}` : ''}${result.chat ? ` — ${result.chat.title}` : ''}` : 'Failed')) : null),
        h(Section, { title: tr('group.title'), desc: tr('group.desc') },
          h('div', { className: 'tgb-status' },
            chatId
              ? h('span', { className: 'tgb-ok' }, tr('group.bound', { title: status?.chat?.title ?? 'group', id: chatId }))
              : h('span', { className: 'tgb-warn' }, tr('group.unbound'))),
          status?.bindCode
            ? h('ol', { className: 'tgb-steps' },
                h('li', null, tr('group.step1')),
                h('li', null, tr('group.step2')),
                h('li', null, tr('group.step3'), ' ', h('span', { className: 'tgb-code' }, `/bind ${status.bindCode.code}`)))
            : null,
          h('div', { className: 'tgb-row' },
            h('button', { type: 'button', className: 'tgb-btn', 'data-tone': chatId ? undefined : 'primary', disabled: busy || !status?.token?.configured, onClick: () => run(() => postJson(ROUTES.bind)) }, tr('group.bind')),
            chatId ? h('button', { type: 'button', className: 'tgb-btn', disabled: busy || disabled, onClick: () => run(() => postJson(ROUTES.unbind)) }, tr('group.unbind')) : null),
          h('div', { className: 'tgb-field' },
            h('label', { htmlFor: 'tgb-users' }, tr('group.users')),
            h('input', {
              id: 'tgb-users',
              className: 'tgb-input',
              value: usersText,
              disabled,
              onChange: (event) => setUsers(event.target.value),
              onBlur: () => {
                if (users === null) return
                const ids = users.split(/[\s,]+/).map(Number).filter((n) => Number.isSafeInteger(n) && n > 0)
                save('allowedUserIds', ids).then(() => setUsers(null))
              },
            }),
            h('div', { className: 'tgb-hint' }, tr('group.usersHint')))),
        h(Section, { title: tr('out.title') }, fields('out')),
        h(Section, { title: tr('top.title') }, fields('top')),
        h(Section, { title: tr('int.title') }, fields('int')),
        h(Section, { title: tr('adv.title') }, fields('adv')))
    }

    function PlaneIcon() {
      return h('svg', { viewBox: '0 0 16 16', width: 14, height: 14, 'aria-hidden': true, fill: 'currentColor' },
        h('path', { d: 'M14.6 2.2 1.9 7.1c-.9.3-.9.9-.2 1.1l3.2 1 1.2 3.8c.2.4.3.6.7.6.3 0 .5-.1.7-.3l1.6-1.5 3.3 2.4c.6.3 1 .2 1.2-.6l2.2-10.3c.2-.9-.3-1.3-1-1.1zM6.1 9.6l6.2-5.4-4.8 6-.2 2z' }))
    }

    /** Composer pill: link to this session's Telegram topic. */
    function TopicPill({ sessionId, t }) {
      const tr = React.useMemo(() => translator(t), [t])
      const [info, setInfo] = React.useState(null)
      React.useEffect(() => {
        if (!sessionId) return undefined
        let live = true
        let timer
        const load = async () => {
          const next = await getJson(`${ROUTES.session}?id=${encodeURIComponent(sessionId)}`)
          if (!live) return
          setInfo(next)
          timer = setTimeout(load, next?.linked ? 30000 : 8000)
        }
        load()
        return () => {
          live = false
          clearTimeout(timer)
        }
      }, [sessionId])
      if (!info?.ok || !info.linked) return null
      return h('a', { className: 'tgb-pill', href: info.link, target: '_blank', rel: 'noreferrer', title: tr('pill.open'), 'data-muted': info.muted === true }, h(PlaneIcon), 'Telegram')
    }

    return {
      inject: ['slots', 'locale'],
      apply(ctx) {
        ctx.effect(() => ctx.locale.register(NS, { en }), 'telegram-bridge: dictionaries')
        ctx.effect(() => {
          document.querySelector(`style[data-plugin-css="${CSS_ID}"]`)?.remove()
          const tag = document.createElement('style')
          tag.dataset.plugin = PKG
          tag.dataset.pluginCss = CSS_ID
          tag.textContent = CSS
          document.head.appendChild(tag)
          return () => tag.remove()
        }, 'telegram-bridge: styles')
        ctx.slots.inject('conversation.composer.dock', () =>
          ctx.slots.register({ name: 'conversation.composer.dock', id: 'telegram-bridge-topic', order: 20, locale: NS }, TopicPill),
        )
        ctx.inject(['configForms'], (scope) => {
          if (scope.configForms === undefined) return
          const shared = scope.configForms.get(ENTRY)
          const form = {
            getSnapshot: () => shared.getSnapshot(),
            subscribe: (listener) => shared.subscribe(listener),
            mutate: (ops, revision) => shared.mutate(ops, revision),
          }
          scope.slots.inject('plugins.bundle.config', () =>
            scope.slots.register({ name: 'plugins.bundle.config', key: PKG, locale: NS, inject: () => ({ form }) }, SettingsPage),
          )
        })
      },
    }
  },
})
