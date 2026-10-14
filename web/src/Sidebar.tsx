// The sidebar: the sessions grouped by project, the buttons that create them, and the way to the
// skill library, the artifacts, the scheduled tasks and the settings.
import { useCallback, useEffect, useRef } from "preact/hooks";
import { t } from "./i18n";
import { useApp } from "./appStore";
import { useBackdropClose } from "./useBackdropClose";
import { useLeave } from "./motion";
import { NewSessionButton } from "./NewSessionButton";
import { SessionRow } from "./SessionRow";
import { ProjectGroup } from "./ProjectGroup";
import {
  IconBook,
  IconClock,
  IconClose,
  IconDownload,
  IconFolderPlus,
} from "./icons";
import { NEW_PROJECT, NEW_SESSION_TITLE } from "./appConst";
import { closesOnPhone, isPhone } from "./viewport";

// The look of the buttons at the bottom of the sidebar.
const FOOT_BUTTON =
  "w-full flex items-center gap-2 px-3 py-2.5 rounded-xl hover:bg-white/5 transition-colors text-sm text-[#e8e8ea]";

// SidebarHeader is the name of the app, its version and the button that closes the sidebar.
function SidebarHeader() {
  const { lists, updates } = useApp();
  const version = updates.updateInfo?.current_version;
  return (
    <div class="flex items-center gap-2 px-4 py-3 border-b border-white/5 flex-none">
      <span class="text-accent text-lg">🐱</span>
      <h1 class="text-base font-semibold tracking-wide">Motita</h1>
      {version && (
        <span class="text-[10px] text-[#7a7a8c] font-mono mt-0.5">
          v{version.replace(/^v/, "")}
        </span>
      )}
      <button
        class="ml-auto p-1.5 rounded-lg hover:bg-white/5 transition-colors"
        onClick={lists.closeSidebar}
        aria-label={t("Close sidebar")}
      >
        <IconClose size={18} />
      </button>
    </div>
  );
}

// NewButtons are the New session and New project buttons.
function NewButtons() {
  const { actions, projects } = useApp();
  const { createSession } = actions;
  const newSession = useCallback(() => createSession(), [createSession]);
  return (
    <div class="px-3 py-2 flex-none flex gap-2">
      <NewSessionButton
        loading={actions.creatingSession === ""}
        onClick={newSession}
        label={t(NEW_SESSION_TITLE)}
      />
      <button
        class="flex items-center justify-center px-3 py-2.5 rounded-xl border border-white/10 text-[#e8e8ea] hover:bg-white/5 active:scale-95 transition-all"
        onClick={projects.openNewProject}
        title={t(NEW_PROJECT)}
      >
        <IconFolderPlus size={16} />
      </button>
    </div>
  );
}

// SectionTitle is the label over one part of the list, so a session and a project are never
// mistaken for each other.
function SectionTitle({ id, label }: { id: string; label: string }) {
  return (
    <h2
      id={id}
      class="px-2 pt-3 pb-1 text-[10px] font-semibold uppercase tracking-wider text-[#7a7a8c]"
    >
      {label}
    </h2>
  );
}

// EmptySection says a part of the list has nothing in it, rather than leaving a blank gap.
function EmptySection({ label }: { label: string }) {
  return (
    <div
      class="mx-1 px-3 py-2 rounded-xl border border-dashed border-white/6 text-xs text-[#7a7a8c] italic"
      data-testid="sidebar-empty"
    >
      {label}
    </div>
  );
}

// SessionList is two labelled sections: the free-standing sessions, then one group per project.
// Each says so when it is empty.
export function SessionList() {
  const { lists } = useApp();
  const loose = lists.sessions.filter((s) => !s.project_id);
  return (
    <div class="flex-1 overflow-y-auto px-2 pb-2">
      <section
        aria-labelledby="sidebar-sessions"
        data-testid="sidebar-sessions"
      >
        <SectionTitle id="sidebar-sessions" label={t("Sessions")} />
        {loose.map((s) => (
          <SessionRow key={s.id} s={s} />
        ))}
        {loose.length === 0 && <EmptySection label={t("No sessions")} />}
      </section>
      <section
        aria-labelledby="sidebar-projects"
        class="mt-2 pt-1 border-t border-white/5"
        data-testid="sidebar-projects"
      >
        <SectionTitle id="sidebar-projects" label={t("Projects")} />
        {lists.projects.map((p) => (
          <ProjectGroup key={p.id} p={p} />
        ))}
        {lists.projects.length === 0 && (
          <EmptySection label={t("No projects")} />
        )}
      </section>
    </div>
  );
}

// UpgradeButton is shown only when a new version is available.
function UpgradeButton() {
  const { updateInfo, openUpgrade } = useApp().updates;
  return (
    <div class="px-3 py-1 border-t border-white/5 flex-none">
      <button
        class="w-full flex items-center gap-2 px-3 py-2.5 rounded-xl bg-accent/10 border border-accent/20 text-accent font-medium hover:bg-accent/20 active:scale-95 transition-all text-sm"
        onClick={openUpgrade}
      >
        <IconDownload size={18} />
        <span class="flex-1 text-left">{t("Upgrade Motita")}</span>
        <span class="text-[10px] font-mono opacity-70">
          {updateInfo?.latest_version}
        </span>
      </button>
    </div>
  );
}

// Settings is the settings button. The modal it opens is drawn by the app (SettingsHost in
// App.tsx), outside the sidebar: the sidebar's frosted glass is a containing block for anything
// `fixed` inside it, and the modal ended up squeezed into the sidebar's width.
function Settings() {
  const { openSettings } = useApp();
  return (
    <button class={FOOT_BUTTON} onClick={openSettings}>
      <svg
        width="18"
        height="18"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
        class="flex-none"
      >
        <circle cx="12" cy="12" r="3" />
        <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" />
      </svg>
      {t("Settings")}
    </button>
  );
}

// SidebarFoot is the skill library, the artifacts, the scheduled tasks and the settings.
function SidebarFoot() {
  const { skills, artifacts, schedules, rs } = useApp();
  return (
    <div class="px-3 py-2 border-t border-white/5 flex-none space-y-1">
      <button
        class={FOOT_BUTTON}
        onClick={() => skills.setShowSkillLibrary(true)}
      >
        <IconBook size={18} />
        {t("Skill library")}
      </button>
      <button
        class={FOOT_BUTTON + " disabled:opacity-40"}
        data-testid="artifacts-button"
        disabled={!rs.sessionId}
        onClick={artifacts.openArtifacts}
      >
        <svg
          width="18"
          height="18"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
          <polyline points="14 2 14 8 20 8" />
        </svg>
        {t("Artifacts")}
      </button>
      <button class={FOOT_BUTTON} onClick={schedules.openScheduledTasks}>
        <IconClock size={18} />
        {t("Scheduled tasks")}
      </button>
      <Settings />
    </div>
  );
}

// useCloseOnPhonePick closes the sidebar on a phone once a session is picked in it, so the reader
// lands on the conversation. The listener sits on the sidebar itself, not on a JSX handler: the
// sidebar is a plain container, and the rows it listens for are buttons, so a click and the
// keyboard (Enter or Space on the row) both reach it.
function useCloseOnPhonePick(close: () => void) {
  const ref = useRef<HTMLElement>(null);
  const closeRef = useRef(close);
  closeRef.current = close;
  useEffect(() => {
    const el = ref.current;
    if (!el) {
      return undefined;
    }
    const onClick = (e: MouseEvent) => {
      if (isPhone() && closesOnPhone(e.target)) {
        closeRef.current();
      }
    };
    el.addEventListener("click", onClick);
    return () => el.removeEventListener("click", onClick);
  }, []);
  return ref;
}

// Sidebar is the sessions list, toggleable on all sizes. On a phone it covers the page, over a
// dimmed layer that closes it.
export function Sidebar() {
  const { lists, updates } = useApp();
  const overlay = useRef<HTMLDivElement>(null);
  useBackdropClose(overlay, lists.closeSidebar);
  const aside = useCloseOnPhonePick(lists.closeSidebar);
  // Closing slides it back out to the left, over a fading overlay on a phone.
  useLeave(aside, "slide-left");
  useLeave(overlay, "fade");
  return (
    <>
      {/* Mobile overlay: click to close the sidebar. */}
      <div ref={overlay} class="fixed inset-0 bg-black/50 z-20 md:hidden" />
      <aside
        class="sidebar frosted fixed md:relative inset-y-0 left-0 w-72 max-w-[85vw] z-30 flex flex-col border-r border-white/5"
        ref={aside}
      >
        <SidebarHeader />
        <NewButtons />
        <SessionList />
        {updates.updateInfo?.update_available && <UpgradeButton />}
        <SidebarFoot />
      </aside>
    </>
  );
}
