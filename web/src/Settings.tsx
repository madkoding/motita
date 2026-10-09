import { useEffect, useState } from 'preact/hooks'
import { t, tf } from './i18n'
import { type Api, type GitAccount, listAccounts, disconnect } from './gitApi'
import type { LangSetting } from './i18n'
import { initSettings, saveSettings, loadNotify, saveNotify, notifyState, type UISettings, type FontSize, type Density } from './settings'

initSettings()

interface Props {
  langSetting: LangSetting
  langAvailable: boolean
  onLang: (s: LangSetting) => void
  onClose: () => void
  // The git host connections section. Absent on a gateway that has no git API.
  git?: {
    api: Api
    // Bumped by the app after a host was connected, so the list is read again.
    rev: number
    onConnect: (service?: string, selfHosted?: { kind: string; host: string }) => void
    onNotice: (message: string, error?: boolean) => void
  }
}

export function SettingsModal({ langSetting, langAvailable, onLang, onClose, git }: Props) {
  const [s, setS] = useState<UISettings>(() => initSettings())
  const update = (next: UISettings) => { setS(next); saveSettings(next) }
  const [notifyOn, setNotifyOn] = useState(() => loadNotify())
  // The browser's answer is read on every render: it can change in the browser's own settings while this is open.
  const permission = typeof Notification !== 'undefined' ? Notification.permission : undefined
  const [, bump] = useState(0)
  const ns = notifyState(permission, notifyOn)
  const sel = 'min-w-0 px-2 py-1 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-hidden focus:border-accent'
  return (
    <div class="fixed inset-0 z-100 flex items-center justify-center bg-black/60" data-testid="settings-modal" onClick={onClose}>
      <div class="w-[90%] max-w-md max-h-[90vh] overflow-y-auto rounded-2xl border border-white/10 bg-surface-solid p-4 space-y-3" role="dialog" aria-label={t('Settings')} onClick={(e) => e.stopPropagation()}>
        <div class="flex items-center">
          <h2 class="flex-1 text-sm font-semibold text-[#e8e8ea]">{t('Settings')}</h2>
          <button class="text-[#9a9aaa] px-2" aria-label={t('Close')} onClick={onClose}>×</button>
        </div>
        {langAvailable && (
          <label class="flex items-center gap-2 text-sm text-[#e8e8ea]">
            <span class="flex-1">{t('Language')}</span>
            <select class={sel} value={langSetting} aria-label={t('Language')} onChange={(e) => onLang((e.target as HTMLSelectElement).value as LangSetting)}>
              <option value="auto">{t('Automatic')}</option>
              <option value="en">English</option>
              <option value="es">Español</option>
            </select>
          </label>
        )}
        <label class="flex items-center gap-2 text-sm text-[#e8e8ea]">
          <span class="flex-1">{t('Font size')}</span>
          <select class={sel} value={s.fontSize} aria-label={t('Font size')} onChange={(e) => update({ ...s, fontSize: (e.target as HTMLSelectElement).value as FontSize })}>
            <option value="small">{t('Small')}</option>
            <option value="normal">{t('Normal')}</option>
            <option value="large">{t('Large')}</option>
          </select>
        </label>
        <label class="flex items-center gap-2 text-sm text-[#e8e8ea]">
          <span class="flex-1">{t('Density')}</span>
          <select class={sel} value={s.density} aria-label={t('Density')} onChange={(e) => update({ ...s, density: (e.target as HTMLSelectElement).value as Density })}>
            <option value="comfortable">{t('Comfortable')}</option>
            <option value="compact">{t('Compact')}</option>
          </select>
        </label>
        <div class="flex items-center gap-2 text-sm text-[#e8e8ea]" data-testid="notifications-setting">
          <span class="flex-1">
            {t('Notify me when a CI ends')}
            <span class="block text-xs text-[#7a7a8c]">
              {t(ns === 'unsupported' ? 'This browser cannot show notifications.'
                : ns === 'blocked' ? 'Blocked: allow notifications for this site in the browser settings.'
                : ns === 'ask' ? 'Only while this tab is in the background. The browser has to allow it first.'
                : 'Only while this tab is in the background.')}
            </span>
          </span>
          {ns === 'ask' && (
            <button
              type="button"
              class={smallBtn}
              onClick={() => { void Notification.requestPermission().then(() => bump(n => n + 1)) }}
            >
              {t('Allow')}
            </button>
          )}
          {(ns === 'on' || ns === 'off') && (
            <input
              type="checkbox"
              aria-label={t('Notify me when a CI ends')}
              checked={ns === 'on'}
              onChange={(e) => { const v = (e.target as HTMLInputElement).checked; setNotifyOn(v); saveNotify(v) }}
            />
          )}
        </div>
        {git && <GitConnections {...git} />}
      </div>
    </div>
  )
}

const inputCls = 'min-w-0 px-2 py-1.5 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-hidden focus:border-accent'
const smallBtn = 'btn-outline px-3 min-h-[32px] rounded-lg border border-white/10 text-xs text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-transform'

// GitConnections lists every git host with its state, and connects, disconnects or adds one.
function GitConnections({ api, rev, onConnect, onNotice }: NonNullable<Props['git']>) {
  const [accounts, setAccounts] = useState<GitAccount[] | null>(null)
  const [err, setErr] = useState('')
  const [confirm, setConfirm] = useState('')
  const [adding, setAdding] = useState(false)
  const [kind, setKind] = useState('gitlab')
  const [host, setHost] = useState('')
  const [reload, setReload] = useState(0)

  useEffect(() => {
    let live = true
    listAccounts(api).then(a => { if (live) { setAccounts(a); setErr('') } }).catch(e => { if (live) { setErr(e instanceof Error ? e.message : String(e)); setAccounts([]) } })
    return () => { live = false }
  }, [rev, reload])

  const doDisconnect = async (a: GitAccount) => {
    try {
      await disconnect(api, a.id)
      onNotice(tf('Disconnected from {host}', { host: a.name }))
    } catch (e) {
      onNotice(e instanceof Error ? e.message : String(e), true)
    }
    setConfirm('')
    setReload(n => n + 1)
  }

  const cleanHost = host.trim().replace(/^https?:\/\//, '').replace(/\/+$/, '')
  return (
    <section class="pt-2 border-t border-white/10 space-y-2" aria-labelledby="git-conn-h" data-testid="git-connections">
      <h3 id="git-conn-h" class="text-sm font-semibold text-[#e8e8ea]">{t('Git connections')}</h3>
      <p class="text-xs text-[#7a7a8c]">{t('Connect a git host to pick your repositories and clone private ones.')}</p>
      {accounts === null && <p class="text-xs text-[#9a9aaa]" role="status">{t('Loading…')}</p>}
      {err && <p class="text-xs text-danger" role="alert">{err}</p>}
      <ul class="space-y-1.5">
        {accounts?.map(a => (
          <li key={a.id} class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-[#e8e8ea]">
            <div class="flex-1 min-w-36">
              <div>{a.name} <span class="text-xs text-[#7a7a8c]">{a.host}</span></div>
              <div class={'text-xs ' + (a.connected ? 'text-[#5fd08a]' : 'text-[#7a7a8c]')}>
                {a.connected ? tf('Connected as {user}', { user: a.username || '…' }) : t('Not connected')}
              </div>
            </div>
            {confirm === a.id ? (
              <>
                <button class={smallBtn + ' text-danger'} onClick={() => doDisconnect(a)}>{t('Confirm')}</button>
                <button class={smallBtn} onClick={() => setConfirm('')}>{t('Cancel')}</button>
              </>
            ) : a.connected ? (
              <>
                <button class={smallBtn} aria-label={tf('Reconnect {host}', { host: a.name })} onClick={() => onConnect(a.id)}>{t('Reconnect')}</button>
                <button class={smallBtn} aria-label={tf('Disconnect {host}', { host: a.name })} onClick={() => setConfirm(a.id)}>{t('Disconnect')}</button>
              </>
            ) : (
              <button class={smallBtn + ' bg-accent text-white border-transparent'} aria-label={tf('Connect {host}', { host: a.name })} onClick={() => onConnect(a.id)}>{t('Connect')}</button>
            )}
          </li>
        ))}
      </ul>
      {adding ? (
        <form class="space-y-2" onSubmit={(e) => { e.preventDefault(); if (cleanHost) { onConnect(undefined, { kind, host: cleanHost }); setAdding(false); setHost('') } }}>
          <div class="flex gap-2">
            <select class={inputCls} value={kind} aria-label={t('Server type')} onChange={(e) => setKind((e.target as HTMLSelectElement).value)}>
              <option value="gitlab">GitLab</option>
              <option value="github">GitHub Enterprise</option>
              <option value="gitea">Gitea / Forgejo</option>
            </select>
            <input class={inputCls + ' flex-1'} value={host} placeholder="git.example.com" aria-label={t('Server address')} autocapitalize="off" spellcheck={false} onInput={(e) => setHost((e.target as HTMLInputElement).value)} />
          </div>
          <div class="flex gap-2">
            <button type="submit" class={smallBtn + ' bg-accent text-white border-transparent'} disabled={!cleanHost}>{t('Continue')}</button>
            <button type="button" class={smallBtn} onClick={() => setAdding(false)}>{t('Cancel')}</button>
          </div>
        </form>
      ) : (
        <button class={smallBtn} onClick={() => setAdding(true)}>{t('Add self-hosted server')}</button>
      )}
    </section>
  )
}
