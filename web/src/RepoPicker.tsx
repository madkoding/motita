import { useEffect, useRef, useState } from 'preact/hooks'
import { t, tf } from './i18n'
import { useDialog } from './useDialog'
import { Spinner } from './GitConnect'
import { type Api, type GitAccount, type GitRepo, GitError, listRepos, mergeRepos, relativeTime } from './gitApi'

export function relTimeText(iso: string | undefined): string {
  const r = relativeTime(iso)
  if (!r) return ''
  switch (r.unit) {
    case 'now': return t('just now')
    case 'minute': return tf('{n}m ago', { n: r.n })
    case 'hour': return tf('{n}h ago', { n: r.n })
    case 'day': return tf('{n}d ago', { n: r.n })
    case 'month': return tf('{n}mo ago', { n: r.n })
    default: return tf('{n}y ago', { n: r.n })
  }
}

interface Props {
  api: Api
  accounts: GitAccount[] // connected hosts only
  initialService?: string
  onPick: (repo: GitRepo, service: string) => void
  onConnectAnother: () => void
  // The host refused the saved credentials: the caller opens the connect flow for it.
  onAuthRequired: (service: string) => void
  onClose: () => void
}

export function RepoPicker({ api, accounts, initialService, onPick, onConnectAnother, onAuthRequired, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  useDialog(ref, onClose)
  const [service, setService] = useState(initialService && accounts.some(a => a.id === initialService) ? initialService : accounts[0]?.id || '')
  const [q, setQ] = useState('')
  const [dq, setDq] = useState('')
  const [repos, setRepos] = useState<GitRepo[]>([])
  const [more, setMore] = useState(false)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [active, setActive] = useState(0)
  const req = useRef(0)

  // Debounce the search box.
  useEffect(() => {
    const id = setTimeout(() => setDq(q), 300)
    return () => clearTimeout(id)
  }, [q])

  const load = async (p: number, append: boolean) => {
    const my = ++req.current
    setLoading(true); setErr('')
    try {
      const r = await listRepos(api, service, dq, p)
      if (my !== req.current) return
      setRepos(prev => append ? mergeRepos(prev, r.repos) : r.repos)
      setMore(r.more); setPage(p)
      if (!append) setActive(0)
    } catch (e) {
      if (my !== req.current) return
      if (e instanceof GitError && e.authRequired) { onAuthRequired(e.service || service); return }
      setErr(e instanceof Error ? e.message : String(e))
    }
    setLoading(false)
  }

  useEffect(() => { if (service) void load(1, false) }, [service, dq])
  useEffect(() => () => { req.current++ }, [])

  const listRef = useRef<HTMLUListElement>(null)
  useEffect(() => {
    listRef.current?.querySelector<HTMLElement>('[aria-selected="true"]')?.scrollIntoView?.({ block: 'nearest' })
  }, [active])

  const pick = (r: GitRepo | undefined) => { if (r) onPick(r, service) }
  const onKey = (e: KeyboardEvent) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); setActive(i => Math.min(i + 1, repos.length - 1)) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); setActive(i => Math.max(i - 1, 0)) }
    else if (e.key === 'Enter') { e.preventDefault(); pick(repos[active]) }
  }

  const current = accounts.find(a => a.id === service)
  return (
    <div class="fixed inset-0 z-110 flex items-center justify-center bg-black/70 backdrop-blur-xs p-4" data-testid="repo-picker" onClick={onClose}>
      <div ref={ref} data-dialog-root class="frosted rounded-2xl border border-white/10 w-full max-w-lg max-h-[85vh] flex flex-col p-5 shadow-2xl" role="dialog" aria-modal="true" aria-label={t('Choose from my repositories')} onClick={(e) => e.stopPropagation()}>
        <div class="flex items-center gap-2 mb-3">
          <h2 class="flex-1 text-base font-semibold text-[#e8e8ea]">{t('Choose from my repositories')}</h2>
          <button class="p-1.5 rounded-lg hover:bg-white/5 text-[#9a9aaa]" aria-label={t('Close')} onClick={onClose}>×</button>
        </div>
        <div class="flex gap-2 mb-3">
          <select class="min-w-0 px-2 py-2 rounded-xl bg-black/30 border border-white/10 text-sm text-[#e8e8ea] focus:outline-hidden focus:border-accent" aria-label={t('Git host')} value={service}
            onChange={(e) => {
              const v = (e.target as HTMLSelectElement).value
              if (v === '__connect') { (e.target as HTMLSelectElement).value = service; onConnectAnother(); return }
              setService(v); setRepos([])
            }}>
            {accounts.map(a => <option key={a.id} value={a.id}>{a.name}{a.username ? ' (' + a.username + ')' : ''}</option>)}
            <option value="__connect">{t('Connect another host…')}</option>
          </select>
          <input class="flex-1 min-w-0 px-3 py-2 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent" type="search" value={q} data-autofocus
            onInput={(e) => setQ((e.target as HTMLInputElement).value)} onKeyDown={onKey}
            placeholder={t('Search repositories')} aria-label={t('Search repositories')}
            role="combobox" aria-expanded="true" aria-controls="repo-list" aria-activedescendant={repos[active] ? 'repo-opt-' + active : undefined} />
        </div>
        <div class="flex-1 min-h-[160px] overflow-y-auto -mx-1 px-1" aria-busy={loading}>
          {err && (
            <div class="p-3 rounded-xl border border-danger/40 text-sm text-danger" role="alert">
              {err}
              <button class="ml-2 underline" onClick={() => load(1, false)}>{t('Retry')}</button>
            </div>
          )}
          {!err && !loading && repos.length === 0 && (
            <p class="py-8 text-center text-sm text-[#7a7a8c]">{dq.trim() ? tf('No repositories match "{q}".', { q: dq.trim() }) : tf('No repositories found in {host}.', { host: current?.name || '' })}</p>
          )}
          <ul id="repo-list" ref={listRef} role="listbox" aria-label={t('Repositories')} class="space-y-1">
            {repos.map((r, i) => (
              <li key={r.clone_url} id={'repo-opt-' + i} role="option" aria-selected={i === active}
                class={'px-3 py-2 rounded-xl cursor-pointer ' + (i === active ? 'bg-white/10' : 'hover:bg-white/5')}
                onMouseEnter={() => setActive(i)} onClick={() => pick(r)}>
                <div class="flex items-center gap-2 text-sm text-[#e8e8ea]">
                  <span class="flex-1 min-w-0 truncate font-medium">{r.full_name}</span>
                  {r.private && (
                    <svg class="flex-none text-[#9a9aaa]" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" role="img" aria-label={t('Private')}>
                      <rect x="3" y="11" width="18" height="11" rx="2" /><path d="M7 11V7a5 5 0 0 1 10 0v4" />
                    </svg>
                  )}
                  <span class="flex-none text-xs text-[#7a7a8c]">{relTimeText(r.updated_at)}</span>
                </div>
                {r.description && <div class="text-xs text-[#9a9aaa] truncate">{r.description}</div>}
              </li>
            ))}
          </ul>
          {loading && <div class="flex items-center justify-center gap-2 py-4 text-sm text-[#9a9aaa]" role="status"><Spinner /> {t('Loading repositories…')}</div>}
          {!loading && more && !err && (
            <button class="w-full mt-2 min-h-[40px] rounded-xl border border-white/10 text-sm text-[#e8e8ea] hover:bg-white/5" onClick={() => load(page + 1, true)}>{t('Load more')}</button>
          )}
        </div>
      </div>
    </div>
  )
}
