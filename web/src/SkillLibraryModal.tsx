// The skill library: the skills the agent has, the ones the review proposed, and the archived ones.
import { t, tf, tc } from './i18n'
import { choose } from './choose'
import { useApp } from './appStore'
import { ModalBackdrop } from './ModalBackdrop'
import { Markdown } from './Markdown'
import { skillHeading } from './appFormat'
import { IconBook, IconClose } from './icons'
import type { SkillInfo } from './appTypes'

const SMALL_BTN = 'px-2.5 py-1 rounded-lg border text-xs active:scale-95 transition-transform'
const WIDE_BTN = 'min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform'

// disableTitle says what turning the skill on or off does.
function disableTitle(disabled = false): string {
  return choose(disabled, t('The agent will see this again'), t('The agent stops seeing this in its index and its search'))
}

// SkillTags are the marks of a skill: who wrote it, whether it is stale, pinned or turned off.
function SkillTags({ s }: { s: SkillInfo }) {
  return (
    <>
      {s.created_by === 'agent' && <span class="skill-tag">{t('agent')}</span>}
      {s.state === 'stale' && <span class="skill-tag skill-tag-warn">{t('stale')}</span>}
      {s.pinned && <span class="skill-tag skill-tag-accent">{t('pinned')}</span>}
      {s.disabled && <span class="skill-tag skill-tag-off">{tc('skill', 'off')}</span>}
    </>
  )
}

// OpenSkillActions are the buttons of the open skill: pin, turn off and delete.
function OpenSkillActions({ skill }: { skill: SkillInfo }) {
  const { skills, deletion } = useApp()
  return (
    <>
      <button
        class={`ml-1 ${SMALL_BTN} border-white/10 text-[#9a9aaa] hover:text-[#e8e8ea]`}
        onClick={() => skills.pinSkill(skill.name, !skill.pinned)}
        title={choose(skill.pinned, t('The curator will leave this alone'), t('Exempt this from every automatic transition'))}
      >
        {choose(skill.pinned, t('Unpin'), t('Pin'))}
      </button>
      <button
        class={`ml-1 ${SMALL_BTN} border-white/10 text-[#9a9aaa] hover:text-[#e8e8ea]`}
        onClick={() => skills.disableSkill(skill.name, !skill.disabled)}
        title={disableTitle(skill.disabled)}
      >
        {choose(skill.disabled, t('Turn on'), t('Turn off'))}
      </button>
      <button
        class={`ml-1 ${SMALL_BTN} border-danger/30 text-danger hover:border-danger/60`}
        onClick={() => deletion.setConfirmDelete({ type: 'skill', id: skill.name, title: skill.title || skill.name })}
      >
        {t('Delete')}
      </button>
    </>
  )
}

// SkillView is one skill, read in full.
function SkillView({ skill }: { skill: SkillInfo }) {
  const { skillBody, setSkillOpen, closeSkillLibrary } = useApp().skills
  return (
    <div>
      <div class="flex items-center gap-2 mb-3 text-xs text-[#7a7a8c]">
        <span class="font-mono">{skill.name}</span>
        <SkillTags s={skill} />
        <span>{tf('used {n}×', { n: skill.use_count })}</span>
      </div>
      <div class="skill-body rounded-xl border border-white/5 p-3">
        <Markdown content={skillBody} />
      </div>
      <div class="flex gap-2 mt-4">
        <button class={'flex-1 ' + WIDE_BTN} onClick={() => setSkillOpen(null)}>
          {t('Back to the list')}
        </button>
        <button class={WIDE_BTN} onClick={closeSkillLibrary}>
          {t('Close')}
        </button>
      </div>
    </div>
  )
}

// SkillItem is one skill of the list: it opens the skill, and turns it on or off.
function SkillItem({ s }: { s: SkillInfo }) {
  const { openSkill, disableSkill } = useApp().skills
  return (
    <li class="flex items-center gap-1">
      <button
        class="flex-1 min-w-0 text-left px-3 py-2.5 rounded-xl border border-white/5 hover:border-white/15 hover:bg-white/3 active:scale-[0.99] transition-all"
        onClick={() => openSkill(s)}
      >
        <div class="flex items-center gap-2">
          <span class="text-sm font-medium text-[#e8e8ea] truncate">{s.title || s.name}</span>
          <SkillTags s={s} />
          <span class="ml-auto text-xs text-[#7a7a8c] shrink-0">{tf('used {n}×', { n: s.use_count })}</span>
        </div>
        {s.summary && (
          <p class="text-xs text-[#9a9aaa] mt-0.5 truncate">{s.summary}</p>
        )}
      </button>
      <button
        class="px-2.5 py-1 rounded-lg border border-white/10 text-xs text-[#9a9aaa] hover:text-[#e8e8ea] shrink-0"
        onClick={() => disableSkill(s.name, !s.disabled)}
        title={disableTitle(s.disabled)}
      >
        {choose(s.disabled, tc('skill-toggle', 'on'), tc('skill-toggle', 'off'))}
      </button>
    </li>
  )
}

// SkillList is the library's skills, filtered by the query.
function SkillList() {
  const { skillsBusy, filteredSkills, skillQuery, skillQueryNorm } = useApp().skills
  if (skillsBusy) {
    return <p class="text-sm text-[#9a9aaa] py-8 text-center">{t('Loading the library…')}</p>
  }
  if (filteredSkills.length === 0) {
    return (
      <p class="text-sm text-[#9a9aaa] py-8 text-center">
        {skillQueryNorm !== '' && tf('Nothing matches “{q}”.', { q: skillQuery.trim() })}
        {skillQueryNorm === '' && t('No skills yet. The agent writes one when it works something out.')}
      </p>
    )
  }
  return (
    <ul class="space-y-1.5">
      {filteredSkills.map((s) => <SkillItem key={s.name} s={s} />)}
    </ul>
  )
}

// ProposedItem is one skill the review proposed: it can be read, accepted or rejected.
function ProposedItem({ name }: { name: string }) {
  const { proposalOpen, openProposal, decideProposal } = useApp().skills
  const open = proposalOpen?.name === name
  return (
    <li class="rounded-xl border border-white/5">
      <div class="flex items-center gap-2 px-3 py-2">
        <button
          class="min-w-0 text-left text-sm text-[#e8e8ea] truncate font-mono hover:underline"
          onClick={() => openProposal(name)}
          aria-expanded={open}
        >
          {name}
        </button>
        <button
          class={`ml-auto ${SMALL_BTN} border-white/10 text-[#e8e8ea] hover:border-accent/40 shrink-0`}
          onClick={() => decideProposal(name, true)}
        >
          {t('Accept')}
        </button>
        <button
          class={`${SMALL_BTN} border-danger/30 text-danger hover:border-danger/60 shrink-0`}
          onClick={() => decideProposal(name, false)}
        >
          {t('Reject')}
        </button>
      </div>
      {open && proposalOpen && (
        <div class="skill-body border-t border-white/5 p-3">
          <Markdown content={proposalOpen.body} />
        </div>
      )}
    </li>
  )
}

// Proposed are the skills the review proposed. No session uses them until they are accepted.
function Proposed({ names }: { names: string[] }) {
  return (
    <div class="mt-5 pt-4 border-t border-white/5">
      <h3 class="text-xs uppercase tracking-wide text-[#7a7a8c] mb-1">{t('Proposed by the review')}</h3>
      <p class="text-xs text-[#9a9aaa] mb-2">{t('No session uses these until you accept them. Read each one first.')}</p>
      <ul class="space-y-1.5">
        {names.map((name) => <ProposedItem key={name} name={name} />)}
      </ul>
    </div>
  )
}

// Archived are the skills the curator archived, each of which can be restored.
function Archived({ names }: { names: string[] }) {
  const { restoreSkill } = useApp().skills
  return (
    <div class="mt-5 pt-4 border-t border-white/5">
      <h3 class="text-xs uppercase tracking-wide text-[#7a7a8c] mb-2">{t('Archived')}</h3>
      <ul class="space-y-1.5">
        {names.map((name) => (
          <li key={name} class="flex items-center gap-2 px-3 py-2 rounded-xl border border-white/5">
            <span class="text-sm text-[#9a9aaa] truncate font-mono">{name}</span>
            <button
              class={`ml-auto ${SMALL_BTN} border-white/10 text-[#e8e8ea] hover:border-accent/40 shrink-0`}
              onClick={() => restoreSkill(name)}
            >
              {t('Restore')}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

// LibraryView is the list of the library, with the filter and the proposed and archived skills.
function LibraryView() {
  const { skillQuery, setSkillQuery, proposedSkills, archivedSkills, closeSkillLibrary } = useApp().skills
  return (
    <div>
      <input
        type="text"
        value={skillQuery}
        onInput={(e) => setSkillQuery((e.target as HTMLInputElement).value)}
        placeholder={t('Filter by name, title or what it is for')}
        class="w-full mb-3 px-3 py-2.5 rounded-xl bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#5a5a68] focus:outline-hidden focus:border-accent/50"
      />
      <SkillList />
      {proposedSkills.length > 0 && <Proposed names={proposedSkills} />}
      {archivedSkills.length > 0 && <Archived names={archivedSkills} />}
      <div class="flex gap-2 mt-5">
        <button class={'flex-1 ' + WIDE_BTN} onClick={closeSkillLibrary}>
          {t('Close')}
        </button>
      </div>
    </div>
  )
}

export function SkillLibraryModal() {
  const { skillOpen, closeSkillLibrary } = useApp().skills
  return (
    <ModalBackdrop onClose={closeSkillLibrary}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-2xl max-h-[80vh] overflow-y-auto p-5 shadow-2xl">
        <div class="flex items-center gap-2 mb-4">
          <IconBook size={20} class="text-accent" />
          <h2 class="text-base font-semibold">{skillHeading(skillOpen)}</h2>
          {skillOpen && <OpenSkillActions skill={skillOpen} />}
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeSkillLibrary}
            aria-label={t('Close')}
          >
            <IconClose size={18} />
          </button>
        </div>
        {!!skillOpen && <SkillView skill={skillOpen} />}
        {!skillOpen && <LibraryView />}
      </div>
    </ModalBackdrop>
  )
}
