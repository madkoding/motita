// The git identity modal: asked when git has no user and a repository has to be created.
import { t } from './i18n'
import { choose } from './choose'
import { useApp } from './appStore'
import { ModalBackdrop } from './ModalBackdrop'

// It sits above the new project modal it was opened from.
const BACKDROP = 'fixed inset-0 bg-black/60 backdrop-blur-xs z-60 flex items-center justify-center p-4'
const FIELD = 'w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent'
const LABEL = 'block text-sm text-[#9a9aaa] mb-1.5'

export function GitIdentityModal() {
  const { closeGitIdentity, gitUserName, setGitUserName, gitUserEmail, setGitUserEmail, creatingProject, createProject } = useApp().projects
  const incomplete = !gitUserName.trim() || !gitUserEmail.includes('@')
  return (
    <ModalBackdrop onClose={closeGitIdentity} class={BACKDROP}>
      <form
        class="frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl"
        onSubmit={(e) => {
          e.preventDefault()
          createProject({ name: gitUserName.trim(), email: gitUserEmail.trim() })
        }}
      >
        <h2 class="text-base font-semibold mb-1">{t('Git user')}</h2>
        <p class="text-sm text-[#9a9aaa] mb-4">
          {t('Git has no user configured. It is saved as your global git identity and signs the commits of every repository.')}
        </p>
        <div class="space-y-4">
          <div>
            <label class={LABEL}>{t('Name')} <span class="text-danger">*</span></label>
            <input
              class={FIELD}
              value={gitUserName}
              onInput={(e) => setGitUserName((e.target as HTMLInputElement).value)}
              placeholder="Ada Lovelace"
              autoFocus
            />
          </div>
          <div>
            <label class={LABEL}>{t('Email')} <span class="text-danger">*</span></label>
            <input
              type="email"
              class={FIELD}
              value={gitUserEmail}
              onInput={(e) => setGitUserEmail((e.target as HTMLInputElement).value)}
              placeholder="ada@example.com"
            />
          </div>
        </div>
        <div class="flex gap-2 mt-5">
          <button
            type="submit"
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
            disabled={creatingProject || incomplete}
          >
            {choose(creatingProject, t('Saving…'), t('Save and create'))}
          </button>
          <button
            type="button"
            class="min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
            onClick={closeGitIdentity}
          >
            {t('Cancel')}
          </button>
        </div>
      </form>
    </ModalBackdrop>
  )
}
