import { useState } from 'preact/hooks'
import { t } from './i18n'
import type { Api } from './gitApi'
import type { LangSetting } from './i18n'
import { initSettings, saveSettings, type UISettings, type FontSize, type Density } from './settings'
import { GitConnectionsModal } from './GitConnectionsModal'

initSettings()

interface Props {
  langSetting: LangSetting
  langAvailable: boolean
  onLang: (s: LangSetting) => void
  onClose: () => void
  // The git host connections entry. Absent on a gateway that has no git API.
  git?: {
    api: Api
    // Bumped by the app after a host was connected, so the list is read again.
    rev: number
    onNotice: (message: string, error?: boolean) => void
  }
}

export function SettingsModal({ langSetting, langAvailable, onLang, onClose, git }: Props) {
  const [s, setS] = useState<UISettings>(() => initSettings())
  const [showGit, setShowGit] = useState(false)
  const update = (next: UISettings) => { setS(next); saveSettings(next) }
  const sel = 'min-w-0 px-2 py-1 rounded-lg bg-black/30 border border-white/10 text-xs text-[#e8e8ea] focus:outline-none focus:border-accent'
  return (
    <div class="fixed inset-0 z-[100] flex items-center justify-center bg-black/60" data-testid="settings-modal" onClick={onClose}>
      <div class="w-[90%] max-w-md max-h-[90vh] overflow-y-auto rounded-2xl border border-white/10 bg-[#16161e] p-4 space-y-3" role="dialog" aria-label={t('Settings')} onClick={(e) => e.stopPropagation()}>
        <div class="flex items-center">
          <h2 class="flex-1 text-sm font-semibold text-[#e8e8ea]">{t('Settings')}</h2>
          <button class="text-[#9a9aaa] px-2" aria-label={t('Close')} onClick={onClose}>×</button>
        </div>
        {langAvailable && (
          <label class="flex items-center gap-2 text-sm text-[#e8e8ea]">
            <span class="flex-1">{t('Language')}</span>
            <select class={sel} value={langSetting} aria-label={t('Language')} onChange={(e) => onLang((e.target as HTMLSelectElement).value as LangSetting)}>
              <option value="auto">{t('Automatic')}</option>
              <option value="en">English</option>
              <option value="es">Español</option>
            </select>
          </label>
        )}
        <label class="flex items-center gap-2 text-sm text-[#e8e8ea]">
          <span class="flex-1">{t('Font size')}</span>
          <select class={sel} value={s.fontSize} aria-label={t('Font size')} onChange={(e) => update({ ...s, fontSize: (e.target as HTMLSelectElement).value as FontSize })}>
            <option value="small">{t('Small')}</option>
            <option value="normal">{t('Normal')}</option>
            <option value="large">{t('Large')}</option>
          </select>
        </label>
        <label class="flex items-center gap-2 text-sm text-[#e8e8ea]">
          <span class="flex-1">{t('Density')}</span>
          <select class={sel} value={s.density} aria-label={t('Density')} onChange={(e) => update({ ...s, density: (e.target as HTMLSelectElement).value as Density })}>
            <option value="comfortable">{t('Comfortable')}</option>
            <option value="compact">{t('Compact')}</option>
          </select>
        </label>
        {git && (
          <button class="w-full px-3 py-2.5 rounded-xl border border-white/10 hover:bg-white/5 text-sm text-[#e8e8ea] text-left" data-testid="open-git-connections" onClick={() => setShowGit(true)}>
            {t('Git connections')}
          </button>
        )}
      </div>
      {git && showGit && <GitConnectionsModal api={git.api} rev={git.rev} onNotice={git.onNotice} onClose={() => setShowGit(false)} />}
    </div>
  )
}
