// The model switcher: the provider, the model and the reasoning level the session runs with.
import { t } from './i18n'
import { choose } from './choose'
import { useApp } from './appStore'
import { ModalBackdrop } from './ModalBackdrop'
import { firstModelOf, modelOptions, reasoningOf } from './appFormat'
import { IconClose, IconMonitor, IconSpinner } from './icons'
import type { ConfigView } from './appTypes'
import { REASONING_LEVELS } from './appConst'
import { switchWarning } from './sessionWatch'

const FIELD = 'w-full px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-[#e8e8ea] focus:outline-hidden focus:border-accent'
const LABEL = 'block text-sm text-[#9a9aaa] mb-1.5'

// modelUnchanged says the draft is what is already saved, so there is nothing to apply.
function modelUnchanged(config: ConfigView | null, provider: string, model: string, reasoning: string): boolean {
  if (provider !== (config?.provider || '')) { return false }
  return model === (config?.model || '') && reasoning === reasoningOf(config)
}

// ProviderField is the provider select. The model is re-chosen rather than carried over: it
// belonged to the provider being left, and the new one may not serve it. Going back to the saved
// provider restores the saved model, so a provider round-trip leaves the draft exactly as it was.
function ProviderField() {
  const { config, draftProvider, setDraftProvider, setDraftModel, providers } = useApp().model
  return (
    <div>
      <label class={LABEL}>{t('Provider')}</label>
      <select
        class={FIELD}
        value={draftProvider}
        onChange={(e) => {
          const newProvider = (e.target as HTMLSelectElement).value
          setDraftProvider(newProvider)
          setDraftModel(choose(newProvider === (config?.provider || ''), config?.model || '', firstModelOf(newProvider, providers)))
        }}
      >
        {providers.map(p => (
          <option key={p.id} value={p.id}>
            {p.name}{choose(p.is_current, ' ' + t('(active)'), '')}{choose(p.key_present, '', ' — ' + t('no key needed'))}
          </option>
        ))}
      </select>
    </div>
  )
}

// ModelField is the model select, or a spinner while the saved provider's models are fetched.
function ModelField() {
  const { config, draftProvider, draftModel, setDraftModel, providers, modelList, fetchingModels } = useApp().model
  const savedProvider = config?.provider || ''
  const loading = fetchingModels && draftProvider === savedProvider
  return (
    <div>
      <label class={LABEL}>{t('Model')}</label>
      {loading && (
        <div class="flex items-center gap-2 px-3 py-2.5 text-sm text-[#9a9aaa]">
          <IconSpinner size={16} class="animate-spin text-accent" />
          <span>{t('Loading models...')}</span>
        </div>
      )}
      {!loading && (
        <select
          class={FIELD + ' font-mono text-sm'}
          value={draftModel}
          onChange={(e) => setDraftModel((e.target as HTMLSelectElement).value)}
        >
          {modelOptions(draftProvider, draftModel, savedProvider, modelList, providers).map(m => (
            <option key={m} value={m}>{m}</option>
          ))}
        </select>
      )}
    </div>
  )
}

// ReasoningField is the reasoning level, one button per level.
function ReasoningField() {
  const { draftReasoning, setDraftReasoning } = useApp().model
  return (
    <div>
      <label class={LABEL}>{t('Reasoning')}</label>
      <div class="reasoning-levels" role="radiogroup" aria-label={t('Reasoning level')}>
        {REASONING_LEVELS.map(level => (
          <button
            key={level}
            type="button"
            role="radio"
            aria-checked={draftReasoning === level}
            class={`reasoning-level${choose(draftReasoning === level, ' is-active', '')}`}
            onClick={() => setDraftReasoning(level)}
          >
            {t(level)}
          </button>
        ))}
      </div>
    </div>
  )
}

// SwitchNote warns, before Done, that the switch lands in a conversation that already talked or
// in a turn still running. The model is this session's own, so the switch is never refused.
function SwitchNote() {
  const { model, rs } = useApp()
  const { config, draftProvider, draftModel } = model
  const changed = draftProvider !== (config?.provider || '') || draftModel !== (config?.model || '')
  const note = switchWarning(changed, rs.messages.length > 0, rs.running)
  if (!note) { return null }
  return <p class="text-xs text-[#f0c040] bg-[#f0c040]/10 rounded-lg px-3 py-2" role="note">⚠ {note}</p>
}

export function ModelSwitcherModal() {
  const { config, draftProvider, draftModel, draftReasoning, savingConfig, closeModelSwitcher, discardModelSwitcher, applyProviderModel } = useApp().model
  const unchanged = modelUnchanged(config, draftProvider, draftModel, draftReasoning)
  return (
    <ModalBackdrop onClose={closeModelSwitcher}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl">
        <div class="flex items-center gap-2 mb-4">
          <IconMonitor size={20} class="text-accent" />
          <h2 class="text-base font-semibold">{t('Model')}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={discardModelSwitcher}
            aria-label={t('Close')}
          >
            <IconClose size={18} />
          </button>
        </div>

        <div class="space-y-4">
          <ProviderField />
          <ModelField />
          <ReasoningField />
          <SwitchNote />
          {config && (
            <div class="text-xs text-[#7a7a8c] space-y-1 pt-2 border-t border-white/5">
              <div>{t('API key')}: {choose(config.api_key_present, '✓ ' + t('set'), '✗ ' + t('missing'))}</div>
            </div>
          )}
        </div>

        <div class="flex gap-2 mt-5">
          <button
            class="flex-1 min-h-[44px] px-5 rounded-xl bg-accent text-white font-semibold active:scale-95 transition-transform disabled:opacity-30 disabled:cursor-not-allowed disabled:saturate-0"
            onClick={() => applyProviderModel()}
            disabled={savingConfig || !draftProvider || unchanged}
          >
            {savingConfig && (
              <IconSpinner size={16} class="animate-spin" style="display:inline-block;vertical-align:middle;margin-right:6px" />
            )}
            {t('Done')}
          </button>
        </div>
      </div>
    </ModalBackdrop>
  )
}
