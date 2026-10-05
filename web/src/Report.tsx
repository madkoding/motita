import { useCallback, useEffect, useRef, useState } from 'preact/hooks'
import { Markdown } from './Markdown'
import { Lightbox } from './Lightbox'
import { t, tf, plural } from './i18n'

// The structured account of a finished task, as the gateway sends it in the `done` event
// (agent.Report in Go). Every field is optional here on purpose: a report from a build that
// predates a field must still draw, and the component owes nothing to a field that is absent.
export interface TaskReport {
  version?: number
  status?: string
  summary?: string
  changes?: { path?: string; kind?: string; description?: string }[]
  verification?: { check?: string; result?: string; evidence?: string }[]
  evidence?: { title?: string; before?: string; after?: string; caption?: string }[]
  risks?: string[]
  next_steps?: string[]
}

const STATUS_TEXT: Record<string, string> = { done: 'Done', partial: 'Partly done', failed: 'Not completed' }
const KIND_TEXT: Record<string, string> = { added: 'new', modified: 'edited', deleted: 'removed', other: 'changed' }
const RESULT_MARK: Record<string, string> = { pass: '✓', fail: '✕', skipped: '–' }

// hasReport says whether there is anything beyond the summary worth a card. A report that is only
// a sentence is drawn as the plain message it always was.
export function hasReport(r?: TaskReport | null): r is TaskReport {
  return !!r && !!(r.changes?.length || r.verification?.length || r.evidence?.length || r.risks?.length || r.next_steps?.length)
}

// ReportCard lays the report out: the verdict first, then the answer, then the proof. The proof
// is the point of it — what changed and what was checked are separate lists, so a reader sees at
// a glance whether the work was verified and by what.
// Evidence images are artifacts of the session, behind the token, so a plain <img src> cannot load
// them: `loadImage` fetches one and hands back an object URL (or null when it cannot be read).
export type LoadImage = (name: string) => Promise<string | null>

function useImage(name: string | undefined, load?: LoadImage) {
  const [src, setSrc] = useState<string | null | undefined>(undefined)
  useEffect(() => {
    let live = true
    setSrc(undefined)
    if (!name || !load) { setSrc(null); return }
    load(name).then(u => { if (live) setSrc(u) }).catch(() => { if (live) setSrc(null) })
    return () => { live = false }
  }, [name, load])
  return src
}

function Shot({ kind, name, label, load, onOpen }: { kind: 'before' | 'after'; name?: string; label: string; load?: LoadImage; onOpen: (src: string, label: string) => void }) {
  const src = useImage(name, load)
  const cap = t(kind === 'before' ? 'Before' : 'After')
  return (
    <figure class={`rpt-shot ${kind}`}>
      <figcaption>{cap}</figcaption>
      {src ? (
        <button type="button" class="rpt-shot-btn" title={t('Click to enlarge')} onClick={() => onOpen(src, `${label} — ${cap}`)}>
          <img src={src} alt={`${label} — ${cap}`} loading="lazy" />
        </button>
      ) : (
        <div class="rpt-shot-empty">{!name ? t('No image') : src === undefined ? t('Loading…') : t('Image not available')}</div>
      )}
    </figure>
  )
}

export function ReportCard({ report, loadImage }: { report: TaskReport; loadImage?: LoadImage }) {
  const [zoom, setZoom] = useState<{ images: { src: string; label: string }[]; start: number } | null>(null)
  const evidence = report.evidence ?? []
  // The object URLs of the images loaded so far, so the preview can offer the other half of a pair.
  const urls = useRef(new Map<string, string>())
  const load = useCallback<LoadImage>(async (name) => {
    if (!loadImage) return null
    const u = await loadImage(name)
    if (u) urls.current.set(name, u)
    return u
  }, [loadImage])
  const status = STATUS_TEXT[report.status ?? ''] ? (report.status as string) : 'done'
  const checks = report.verification ?? []
  const changes = report.changes ?? []
  const risks = report.risks ?? []
  const next = report.next_steps ?? []
  const passed = checks.filter(c => c.result === 'pass').length
  const failed = checks.filter(c => c.result === 'fail').length
  return (
    <div class={`rpt rpt-${status}`}>
      <div class="rpt-head">
        <span class="rpt-badge">{t(STATUS_TEXT[status])}</span>
        {checks.length > 0 && (
          <span class={`rpt-tally${failed ? ' bad' : ''}`} title={t('checks that passed')}>
            {tf('{passed}/{total} checks passed', { passed, total: checks.length })}
          </span>
        )}
        {changes.length > 0 && (
          <span class="rpt-tally">{plural(changes.length, '1 change', '{n} changes')}</span>
        )}
      </div>
      {report.summary && <div class="rpt-summary"><Markdown content={report.summary} /></div>}

      {changes.length > 0 && (
        <section class="rpt-sec">
          <h4>{t('What changed')}</h4>
          <ul class="rpt-changes">
            {changes.map((c, i) => (
              <li key={i}>
                <span class={`rpt-kind ${c.kind ?? 'other'}`}>{t(KIND_TEXT[c.kind ?? 'other'] ?? 'changed')}</span>
                <span class="rpt-body">
                  {c.path && <code class="rpt-path">{c.path}</code>}
                  {c.description && <span class="rpt-desc">{c.description}</span>}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {checks.length > 0 && (
        <section class="rpt-sec">
          <h4>{t('How it was checked')}</h4>
          <ul class="rpt-checks">
            {checks.map((c, i) => (
              <li key={i} class={c.result ?? 'skipped'}>
                <span class="rpt-mark" aria-label={t(c.result ?? 'skipped')}>{RESULT_MARK[c.result ?? 'skipped'] ?? '–'}</span>
                <span class="rpt-body">
                  <span class="rpt-check">{c.check}</span>
                  {c.evidence && <span class="rpt-evidence">{c.evidence}</span>}
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}

      {evidence.length > 0 && (
        <section class="rpt-sec">
          <h4>{t('Before and after')}</h4>
          {evidence.map((e, i) => {
            const label = e.title || t('Screenshot')
            // The preview walks the pair: the other half is one arrow away. A half that has not
            // loaded yet is simply not in the set.
            const open = (kind: 'before' | 'after') => (src: string, lbl: string) => {
              const other = kind === 'before' ? e.after : e.before
              const otherSrc = other ? urls.current.get(other) : undefined
              const mine = { src, label: lbl }
              const pair = otherSrc
                ? (kind === 'before' ? [mine, { src: otherSrc, label: `${label} — ${t('After')}` }] : [{ src: otherSrc, label: `${label} — ${t('Before')}` }, mine])
                : [mine]
              setZoom({ images: pair, start: kind === 'after' && otherSrc ? 1 : 0 })
            }
            return (
              <div class="rpt-evidence-set" key={i}>
                {e.title && <div class="rpt-evidence-title">{e.title}</div>}
                <div class="rpt-shots">
                  {e.before !== undefined && e.before !== '' && <Shot kind="before" name={e.before} label={label} load={load} onOpen={open('before')} />}
                  {e.after !== undefined && e.after !== '' && <Shot kind="after" name={e.after} label={label} load={load} onOpen={open('after')} />}
                </div>
                {e.caption && <div class="rpt-evidence-caption">{e.caption}</div>}
              </div>
            )
          })}
        </section>
      )}

      {risks.length > 0 && (
        <section class="rpt-sec rpt-risks">
          <h4>{t('Worth knowing')}</h4>
          <ul>{risks.map((r, i) => <li key={i}>{r}</li>)}</ul>
        </section>
      )}

      {next.length > 0 && (
        <section class="rpt-sec rpt-next">
          <h4>{t('Next')}</h4>
          <ul>{next.map((n, i) => <li key={i}>{n}</li>)}</ul>
        </section>
      )}
      {zoom && <Lightbox images={zoom.images} start={zoom.start} onClose={() => setZoom(null)} />}
    </div>
  )
}
