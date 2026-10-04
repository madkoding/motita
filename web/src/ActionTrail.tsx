import { useState } from 'preact/hooks'
import type { ComponentChildren } from 'preact'
import { plural, t } from './i18n'

// actionLabel is the counter shown while the list of actions is collapsed.
export function actionLabel(n: number): string {
  return plural(n, '1 action', '{n} actions')
}

// ActionTrail shows the commands and actions of a turn as a counter. The detail is
// collapsed by default and opens or closes when the counter is clicked.
export function ActionTrail({ count, children }: { count: number; children: ComponentChildren }) {
  const [open, setOpen] = useState(false)
  return (
    <div class="action-trail">
      <button
        type="button"
        class="action-trail-toggle"
        aria-expanded={open}
        onClick={() => setOpen(o => !o)}
      >
        <span class="action-trail-caret" aria-hidden="true">{open ? '▾' : '▸'}</span>
        <span class="action-trail-count">{actionLabel(count)}</span>
      </button>
      {open && <div class="activity-trail" aria-label={t('steps')}>{children}</div>}
    </div>
  )
}
