import { useCallback, useEffect, useMemo, useRef, useState } from 'preact/hooks'
import { Markdown } from './Markdown'
import { Lightbox } from './Lightbox'
import { t, tf, plural } from './i18n'
import { choose } from './choose'

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
  if (!r) { return false }
  const counts = [r.changes?.length, r.verification?.length, r.evidence?.length, r.risks?.length, r.next_steps?.length]
  return counts.some(n => !!n)
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
    if (!name || !load) { setSrc(null); return undefined }
    load(name).then(u => { if (live) { setSrc(u) } }).catch(() => { if (live) { setSrc(null) } })
    return () => { live = false }
  }, [name, load])
  return src
}

function Shot({ kind, name, label, load, onOpen }: { kind: 'before' | 'after'; name?: string; label: string; load?: LoadImage; onOpen: (src: string, label: string) => void }) {
  const src = useImage(name, load)
  const cap = choose(kind === 'before', t('Before'), t('After'))
  return (
    <figure class={`rpt-shot ${kind}`}>
      <figcaption>{cap}</figcaption>
      {!!src && (
        <button type="button" class="rpt-shot-btn" title={t('Click to enlarge')} onClick={() => onOpen(src, `${label} — ${cap}`)}>
          <img src={src} alt={`${label} — ${cap}`} loading="lazy" />
        </button>
      )}
      {!src && (
        <div class="rpt-shot-empty">{choose(!name, t('No image'), choose(src === undefined, t('Loading…'), t('Image not available')))}</div>
      )}
    </figure>
  )
}

type Change = NonNullable<TaskReport['changes']>[number]
type Check = NonNullable<TaskReport['verification']>[number]
type Evidence = NonNullable<TaskReport['evidence']>[number]
type Zoom = { images: { src: string; label: string }[]; start: number }

// keyed gives each item of a list a key made of its content and of how many identical items came
// before it, so two equal lines still get keys of their own. It runs once per report, not per draw.
function keyed<T>(items: T[] | undefined, content: (item: T) => string): { key: string; item: T }[] {
  const seen = new Map<string, number>()
  return (items ?? []).map(item => {
    const c = content(item)
    const n = seen.get(c) ?? 0
    seen.set(c, n + 1)
    return { key: `${c}#${n}`, item }
  })
}

// keyedReport is the report's lists with their keys.
function keyedReport(report: TaskReport) {
  return {
    changes: keyed(report.changes, c => `${c.kind}|${c.path}|${c.description}`),
    checks: keyed(report.verification, c => `${c.result}|${c.check}|${c.evidence}`),
    evidence: keyed(report.evidence, e => `${e.title}|${e.before}|${e.after}|${e.caption}`),
    risks: keyed(report.risks, r => r),
    next: keyed(report.next_steps, n => n),
  }
}

export function ReportCard({ report, loadImage }: { report: TaskReport; loadImage?: LoadImage }) {
  const [zoom, setZoom] = useState<Zoom | null>(null)
  const closeZoom = useCallback(() => setZoom(null), [])
  // The object URLs of the images loaded so far, so the preview can offer the other half of a pair.
  const urls = useRef(new Map<string, string>())
  const load = useCallback<LoadImage>(async (name) => {
    if (!loadImage) { return null }
    const u = await loadImage(name)
    if (u) { urls.current.set(name, u) }
    return u
  }, [loadImage])
  const status = choose(STATUS_TEXT[report.status ?? ''], report.status as string, 'done')
  const { changes, checks, evidence, risks, next } = useMemo(() => keyedReport(report), [report])
  return (
    <div class={`rpt rpt-${status}`}>
      <ReportHead status={status} checks={report.verification ?? []} changes={changes.length} />
      {report.summary && <div class="rpt-summary"><Markdown content={report.summary} /></div>}

      {changes.length > 0 && <ChangesSection changes={changes} />}
      {checks.length > 0 && <ChecksSection checks={checks} />}

      {evidence.length > 0 && (
        <section class="rpt-sec">
          <h4>{t('Before and after')}</h4>
          {evidence.map(({ key, item }) => <EvidenceSet key={key} e={item} load={load} urls={urls} onZoom={setZoom} />)}
        </section>
      )}

      {risks.length > 0 && <BulletSection cls="rpt-risks" title={t('Worth knowing')} items={risks} />}
      {next.length > 0 && <BulletSection cls="rpt-next" title={t('Next')} items={next} />}
      {zoom && <Lightbox images={zoom.images} start={zoom.start} onClose={closeZoom} />}
    </div>
  )
}

// ReportHead is the verdict and the tallies.
function ReportHead({ status, checks, changes }: { status: string; checks: Check[]; changes: number }) {
  const passed = checks.filter(c => c.result === 'pass').length
  const failed = checks.filter(c => c.result === 'fail').length
  return (
    <div class="rpt-head">
      <span class="rpt-badge">{t(STATUS_TEXT[status])}</span>
      {checks.length > 0 && (
        <span class={`rpt-tally${choose(failed, ' bad', '')}`} title={t('checks that passed')}>
          {tf('{passed}/{total} checks passed', { passed, total: checks.length })}
        </span>
      )}
      {changes > 0 && (
        <span class="rpt-tally">{plural(changes, '1 change', '{n} changes')}</span>
      )}
    </div>
  )
}

function ChangesSection({ changes }: { changes: { key: string; item: Change }[] }) {
  return (
    <section class="rpt-sec">
      <h4>{t('What changed')}</h4>
      <ul class="rpt-changes">
        {changes.map(({ key, item: c }) => (
          <li key={key}>
            <span class={`rpt-kind ${c.kind ?? 'other'}`}>{t(KIND_TEXT[c.kind ?? 'other'] ?? 'changed')}</span>
            <span class="rpt-body">
              {c.path && <code class="rpt-path">{c.path}</code>}
              {c.description && <span class="rpt-desc">{c.description}</span>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

function ChecksSection({ checks }: { checks: { key: string; item: Check }[] }) {
  return (
    <section class="rpt-sec">
      <h4>{t('How it was checked')}</h4>
      <ul class="rpt-checks">
        {checks.map(({ key, item: c }) => (
          <li key={key} class={c.result ?? 'skipped'}>
            <span class="rpt-mark" aria-label={t(c.result ?? 'skipped')}>{RESULT_MARK[c.result ?? 'skipped'] ?? '–'}</span>
            <span class="rpt-body">
              <span class="rpt-check">{c.check}</span>
              {c.evidence && <span class="rpt-evidence">{c.evidence}</span>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  )
}

// EvidenceSet is one before/after pair with its title and caption.
function EvidenceSet({ e, load, urls, onZoom }: { e: Evidence; load: LoadImage; urls: { current: Map<string, string> }; onZoom: (z: Zoom) => void }) {
  const label = e.title || t('Screenshot')
  // The preview walks the pair: the other half is one arrow away. A half that has not
  // loaded yet is simply not in the set.
  const open = (kind: 'before' | 'after') => (src: string, lbl: string) => {
    const other = choose(kind === 'before', e.after, e.before)
    let otherSrc: string | undefined
    if (other) { otherSrc = urls.current.get(other) }
    const mine = { src, label: lbl }
    let pair = [mine]
    let start = 0
    if (otherSrc && kind === 'before') {
      pair = [mine, { src: otherSrc, label: `${label} — ${t('After')}` }]
    } else if (otherSrc) {
      pair = [{ src: otherSrc, label: `${label} — ${t('Before')}` }, mine]
      start = 1
    } else {
      // Only this half has loaded: the preview shows it alone.
    }
    onZoom({ images: pair, start })
  }
  return (
    <div class="rpt-evidence-set">
      {e.title && <div class="rpt-evidence-title">{e.title}</div>}
      <div class="rpt-shots">
        {e.before !== undefined && e.before !== '' && <Shot kind="before" name={e.before} label={label} load={load} onOpen={open('before')} />}
        {e.after !== undefined && e.after !== '' && <Shot kind="after" name={e.after} label={label} load={load} onOpen={open('after')} />}
      </div>
      {e.caption && <div class="rpt-evidence-caption">{e.caption}</div>}
    </div>
  )
}

// BulletSection is a titled list of plain lines: the risks, the next steps.
function BulletSection({ cls, title, items }: { cls: string; title: string; items: { key: string; item: string }[] }) {
  return (
    <section class={`rpt-sec ${cls}`}>
      <h4>{title}</h4>
      <ul>{items.map(({ key, item }) => <li key={key}>{item}</li>)}</ul>
    </section>
  )
}
