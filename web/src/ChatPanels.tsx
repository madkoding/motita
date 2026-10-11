// What sits over and under the conversation: the session modal while a conversation is being put
// on screen, and the approval panel while a command waits for the person's answer.
import { t } from './i18n'
import { choose } from './choose'
import { useApp } from './appStore'
import { useListener } from './domHooks'
import type { PendingApproval } from './appTypes'
import { APPROVAL_ALL, APPROVAL_NO, APPROVAL_RUN, NEEDS_APPROVAL } from './appConst'

// ChatLoadingModal is a small centred card, not a full-bleed overlay.
// It says what is happening ("Loading session") rather than covering the page, and
// the mark turns while the transcript arrives and its formulas, diagrams and emoji
// are built. It is a SIBLING of the scroll container rather than a child, because a
// child of a scrolling box scrolls away with the content — and this has to stay put
// while the page underneath changes height.
export function ChatLoadingModal() {
  const { modalLeaving } = useApp().scroll
  return (
    <div class={`chat-modal-scrim${choose(modalLeaving, ' chat-modal-leaving', '')}`}>
      <output class="chat-modal" aria-live="polite">
        <span class="chat-spinner" aria-hidden="true">
          <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg" focusable="false">
            <path d="M12,23a9.63,9.63,0,0,1-8-9.5,9.51,9.51,0,0,1,6.79-9.1A1.66,1.66,0,0,0,12,2.81h0a1.67,1.67,0,0,0-1.94-1.64A11,11,0,0,0,12,23Z">
              <animateTransform attributeName="transform" type="rotate" dur="0.75s"
                values="0 12 12;360 12 12" repeatCount="indefinite" />
            </path>
          </svg>
        </span>
        <span class="chat-modal-label">{t('Loading session')}</span>
      </output>
    </div>
  )
}

// The look of the three answers, without the highlight.
const RUN_CLASS = 'min-h-[44px] min-w-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform'
const ALL_CLASS = 'min-h-[44px] min-w-[44px] px-4 rounded-xl border border-accent/40 text-accent font-semibold active:scale-95 transition-transform'
const NO_CLASS = 'min-h-[44px] min-w-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform'

// ApprovalPanel is the question a command is waiting on, solid opaque, above the gradient. It takes
// focus when a question arrives, so the arrows move between the answers and Enter takes one.
export function ApprovalPanel({ approval }: { approval: PendingApproval }) {
  const { rs, shell } = useApp()
  const { approvalSel, setApprovalSel, approvalRef } = rs
  const { answerApproval } = shell
  useListener(approvalRef, 'keydown', shell.onApprovalKey)
  const picked = (i: number) => choose(approvalSel === i, ' is-picked', '')
  return (
    <fieldset
      ref={approvalRef}
      tabIndex={-1}
      aria-label={t(NEEDS_APPROVAL)}
      class="approval-panel frosted flex-none px-4 sm:px-5 py-3.5 border-t-2 border-warning z-10"
    >
      <h2 class="text-sm mb-2 text-warning">
        {approval.question ?? approval.reason ?? t(NEEDS_APPROVAL)}
      </h2>
      <pre class="whitespace-pre-wrap break-all max-h-[35vh] overflow-y-auto mb-2.5 p-3 bg-black/20 border border-white/5 rounded-lg font-mono text-[13px]">
        {approval.command ?? ''}
      </pre>
      <div class="flex flex-wrap gap-2">
        <button
          class={RUN_CLASS + picked(APPROVAL_RUN)}
          aria-pressed={approvalSel === APPROVAL_RUN}
          onMouseEnter={() => setApprovalSel(APPROVAL_RUN)}
          onClick={() => answerApproval(approval.id, true)}
        >
          {t('Run it')}
        </button>
        <button
          class={ALL_CLASS + picked(APPROVAL_ALL)}
          aria-pressed={approvalSel === APPROVAL_ALL}
          onMouseEnter={() => setApprovalSel(APPROVAL_ALL)}
          title={t('Run this and every later command in this session without asking. Commands the policy forbids stay forbidden.')}
          onClick={() => answerApproval(approval.id, true, true)}
        >
          {t('Allow all this session')}
        </button>
        <button
          class={NO_CLASS + picked(APPROVAL_NO)}
          aria-pressed={approvalSel === APPROVAL_NO}
          onMouseEnter={() => setApprovalSel(APPROVAL_NO)}
          onClick={() => answerApproval(approval.id, false)}
        >
          {t('No')}
        </button>
      </div>
    </fieldset>
  )
}
