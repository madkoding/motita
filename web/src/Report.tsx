import { Markdown } from './Markdown'
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
  risks?: string[]
  next_steps?: string[]
}

const STATUS_TEXT: Record<string, string> = { done: 'Done', partial: 'Partly done', failed: 'Not completed' }
const KIND_TEXT: Record<string, string> = { added: 'new', modified: 'edited', deleted: 'removed', other: 'changed' }
const RESULT_MARK: Record<string, string> = { pass: '✓', fail: '✕', skipped: '–' }

// hasReport says whether there is anything beyond the summary worth a card. A report that is only
// a sentence is drawn as the plain message it always was.
export function hasReport(r?: TaskReport | null): r is TaskReport {
  return !!r && !!(r.changes?.length || r.verification?.length || r.risks?.length || r.next_steps?.length)
}

// ReportCard lays the report out: the verdict first, then the answer, then the proof. The proof
// is the point of it — what changed and what was checked are separate lists, so a reader sees at
// a glance whether the work was verified and by what.
export function ReportCard({ report }: { report: TaskReport }) {
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
    </div>
  )
}
