import { h } from 'preact'

/**
 * The "+" button that opens a new session.
 *
 * Creating a session inside a PROJECT is not instant: the gateway pulls/fetches
 * the repository before it answers, which can take seconds. While that runs the
 * button shows a spinner in place of the plus and is disabled, so the click is
 * visibly acknowledged and cannot be repeated.
 *
 * `compact` is the small icon-only variant used in a project header; the default
 * is the full-width "New session" button in the sidebar footer.
 *
 * Written with `h` rather than JSX so the node test can import it directly: the
 * test runner strips types but does not transform JSX.
 */
export function NewSessionButton({ loading, onClick, compact, title, label }: {
  loading: boolean
  onClick: (e: MouseEvent) => void
  compact?: boolean
  title?: string
  label?: string
}) {
  const icon = loading
    ? h('span', { class: 'btn-spinner', 'aria-hidden': 'true' })
    : h('svg', {
        width: compact ? 12 : 16,
        height: compact ? 12 : 16,
        viewBox: '0 0 24 24',
        fill: 'none',
        stroke: 'currentColor',
        'stroke-width': compact ? 2.5 : 2,
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
      },
      h('line', { x1: 12, y1: 5, x2: 12, y2: 19 }),
      h('line', { x1: 5, y1: 12, x2: 19, y2: 12 }),
    )

  if (compact) {
    return h('button', {
      type: 'button',
      class: 'p-0.5 rounded-sm hover:bg-white/10 opacity-0 group-hover:opacity-100 transition-opacity flex-none disabled:opacity-60',
      title,
      onClick,
      disabled: loading,
    }, icon)
  }
  return h('button', {
    type: 'button',
    class: 'btn-cut flex-1 flex items-center justify-center gap-2 px-3 py-2.5 bg-accent/10 border border-accent/20 text-accent font-medium hover:bg-accent/20 active:scale-95 transition-all disabled:opacity-60 disabled:cursor-wait',
    onClick,
    disabled: loading,
  }, icon, label)
}
