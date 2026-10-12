// The blocking modal shown while the browser has no valid credential. It covers the entire screen
// and cannot be dismissed without a token.
import { useRef } from 'preact/hooks'
import { t } from './i18n'
import { useApp } from './appStore'
import { useStopClicks } from './domHooks'
import { IconAlert, IconSpinner } from './icons'

// AuthForm is where the token is typed.
function AuthForm() {
  const { authInput, setAuthInput, authBusy, authError, submitAuth } = useApp().auth
  return (
    <form
      onSubmit={(e) => { e.preventDefault(); submitAuth() }}
    >
      <label class="block text-sm text-[#9a9aaa] mb-1.5" htmlFor="auth-token">{t('Token or password')}</label>
      <input
        id="auth-token"
        type="password"
        class="w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent font-mono text-sm"
        value={authInput}
        onInput={(e) => setAuthInput((e.target as HTMLInputElement).value)}
        placeholder={t('Paste the gateway token…')}
        autoFocus
        autoComplete="off"
        spellcheck={false}
        disabled={authBusy}
      />
      {authError && (
        <p class="text-sm text-danger mt-2 flex items-center gap-1.5">
          <IconAlert size={14} />
          {authError}
        </p>
      )}
      <p class="text-xs text-[#7a7a8c] mt-3 leading-relaxed">
        {t('Run')} <code class="font-mono text-accent bg-accent/10 px-1 rounded-sm">motita gateway start</code> {t('in a terminal to print the link, or paste the token here.')}
      </p>
      <button
        type="submit"
        disabled={authBusy || !authInput.trim()}
        class="w-full min-h-[44px] mt-4 rounded-xl bg-accent text-white font-semibold disabled:opacity-30 disabled:cursor-not-allowed active:scale-95 transition-transform flex items-center justify-center gap-2"
      >
        {authBusy && (
          <>
            <IconSpinner class="animate-spin" size={16} />
            {t('Connecting…')}
          </>
        )}
        {!authBusy && t('Connect')}
      </button>
    </form>
  )
}

export function AuthModal() {
  const { authState } = useApp().auth
  const ref = useRef<HTMLDivElement>(null)
  useStopClicks(ref)
  return (
    <div
      ref={ref}
      class="fixed inset-0 z-100 bg-black/80 backdrop-blur-xs flex items-center justify-center p-4"
    >
      <div class="frosted rounded-2xl border border-accent/20 w-full max-w-sm p-6 shadow-2xl">
        <div class="flex items-center gap-3 mb-4">
          <div class="w-12 h-12 rounded-full bg-accent/15 flex items-center justify-center flex-none">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="text-accent">
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
              <path d="M7 11V7a5 5 0 0 1 10 0v4" />
            </svg>
          </div>
          <div>
            <h2 class="text-base font-semibold text-[#e8e8ea]">{t('Authentication required')}</h2>
            <p class="text-xs text-[#9a9aaa] mt-0.5">{t('Enter the gateway token to continue')}</p>
          </div>
        </div>

        {authState === 'checking' && (
          <div class="flex items-center justify-center gap-2 py-6 text-sm text-[#9a9aaa]">
            <IconSpinner class="animate-spin text-accent" size={18} />
            <span>{t('Checking credentials…')}</span>
          </div>
        )}
        {authState !== 'checking' && <AuthForm />}
      </div>
    </div>
  )
}
