// A project in the sidebar: a collapsible container with its sessions nested inside. The header
// toggles the session list; its action buttons are separate so they do not toggle on touch.
import { useCallback, useRef } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { useLongPress } from "./domHooks";
import { changesTitle, shortBranch } from "./appFormat";
import { NewSessionButton } from "./NewSessionButton";
import { DeleteItem, RowMenu, SessionRow } from "./SessionRow";
import { IconDots, IconFolder } from "./icons";
import type { ProjectInfo } from "./appTypes";
import { EDIT_PROJECT } from "./appConst";

const ITEM_PLAIN =
  "row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm text-[#e8e8ea] hover:bg-white/5";

// ProjectMenu is the "⋯" menu of a project.
function ProjectMenu({ p }: { p: ProjectInfo }) {
  const { menus, projects } = useApp();
  return (
    <RowMenu>
      <button
        class={ITEM_PLAIN}
        data-testid="edit-project"
        onClick={(e) => {
          e.stopPropagation();
          void projects.openEditProject(p);
          menus.closeRowMenu();
        }}
      >
        {t(EDIT_PROJECT)}
      </button>
      {(projects.hasPodman || p.podman) && (
        <button
          class={ITEM_PLAIN}
          data-testid="podman-toggle"
          onClick={(e) => {
            e.stopPropagation();
            void projects.setProjectPodman(p.id, !p.podman);
            menus.closeRowMenu();
          }}
        >
          {choose(p.podman, t("Stop using podman"), t("Run with podman"))}
        </button>
      )}
      <DeleteItem type="project" id={p.id} title={p.title} />
    </RowMenu>
  );
}

// ProjectFacts is the facts line, labelled and coloured exactly like a
// session's: `project` says this is the project's own
// checkout rather than a session's.
//
// There is deliberately NO session/worktree count
// here. The counts were the width the user asked to be
// rid of, and they are visible without them: the
// sessions are the rows under this header, and a
// session's worktree is named by its own row. The
// gateway still reports `sessions`/`worktrees` in
// GET /v1/projects for clients that want them.
function ProjectFacts({ p }: { p: ProjectInfo }) {
  return (
    <div class="flex items-center gap-x-1.5 gap-y-1 flex-wrap mt-1 text-[10px]">
      {p.branch && (
        <span
          class="inline-flex items-center gap-1 flex-none px-1 py-px rounded-sm bg-accent/10"
          title={tf("Project checkout is on branch: {branch}", {
            branch: p.branch,
          })}
        >
          <span class="text-accent/50 uppercase tracking-wide text-[9px]">
            {t("project")}
          </span>
          <span class="font-mono text-accent truncate max-w-28">
            {shortBranch(p.branch)}
          </span>
        </span>
      )}
      {p.podman && (
        <span
          class="inline-flex items-center flex-none px-1 py-px rounded-sm bg-accent/10 text-accent text-[9px] uppercase tracking-wide"
          title={t("This project is run with podman")}
        >
          podman
        </span>
      )}
      {!!p.changes && (
        <span
          class="inline-flex items-center gap-1 flex-none px-1 py-px rounded-sm bg-[#f0a040]/10"
          title={tf("The project checkout has {what}", {
            what: changesTitle(p.changes),
          })}
        >
          <span class="text-[#f0a040]/50 uppercase tracking-wide text-[9px]">
            {choose(p.changes === 1, t("change"), t("changes"))}
          </span>
          <span class="tabular-nums text-[#f0a040]">{p.changes}</span>
        </span>
      )}
    </div>
  );
}

// ProjectActions is the header's "+" and "⋯" buttons. They stop the click, so they do not toggle.
function ProjectActions({ p }: { p: ProjectInfo }) {
  const { menus, actions } = useApp();
  const { createSession } = actions;
  const open = menus.rowMenu?.id === p.id;
  const newSession = useCallback(
    (e: MouseEvent) => {
      e.stopPropagation();
      createSession(p.id);
    },
    [createSession, p.id],
  );
  return (
    <>
      <NewSessionButton
        compact
        loading={actions.creatingSession === p.id}
        onClick={newSession}
        title={t("New session in project")}
      />
      <div
        class={`row-actions relative flex-none${choose(open, " row-actions-open", "")}`}
      >
        <button
          class="p-0.5 rounded-sm hover:bg-white/10"
          title={t("More actions")}
          data-row-menu-trigger
          onClick={(e) => {
            e.stopPropagation();
            menus.toggleRowMenu(e.currentTarget as HTMLElement, {
              type: "project",
              id: p.id,
              title: p.title,
            });
          }}
        >
          <IconDots size={14} />
        </button>
        {open && <ProjectMenu p={p} />}
      </div>
    </>
  );
}

// ProjectHeader is the header of a project's container: a click on it folds or unfolds the list.
function ProjectHeader({
  p,
  collapsed,
}: {
  p: ProjectInfo;
  collapsed: boolean;
}) {
  const { lists, menus } = useApp();
  const ref = useRef<HTMLDivElement>(null);
  useLongPress(ref, (x, y) =>
    menus.setContextMenu({ type: "project", id: p.id, title: p.title, x, y }),
  );
  return (
    <div
      ref={ref}
      class="project-header group flex items-center gap-1.5 px-2.5 py-2 cursor-pointer select-none hover:bg-white/4 transition-colors"
    >
      {/* The header folds its list from anywhere on it: this button covers the header, and its
          own buttons are drawn above it. */}
      <button
        type="button"
        class="row-hit"
        aria-label={p.title}
        aria-expanded={!collapsed}
        onClick={() => lists.toggleProject(p.id)}
      />
      {/* The arrow turns to follow the list: it points RIGHT
          when the sessions are hidden and DOWN when they are
          showing. One path that rotates, not two paths swapped,
          because a swap cannot be animated. The rotation lives
          in `.project-chevron` - this build disables Tailwind's
          `transform` plugin, so a `rotate-90` utility would emit
          a declaration the browser drops. */}
      <svg
        width="12"
        height="12"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2.5"
        stroke-linecap="round"
        stroke-linejoin="round"
        class={`project-chevron flex-none text-[#8a8a9a]${choose(collapsed, " is-collapsed", "")}`}
      >
        <polyline points="6 9 12 15 18 9" />
      </svg>
      <IconFolder size={14} class="flex-none text-accent/60" />
      {/* Title + facts, the same two-line shape as a session
          row so the eye reads one pattern. */}
      <div class="flex-1 min-w-0">
        <div class="truncate font-semibold text-[13px] text-[#c8c8d2]">
          {p.title}
        </div>
        <ProjectFacts p={p} />
      </div>
      <ProjectActions p={p} />
    </div>
  );
}

// ProjectGroup is a project and its sessions.
//
// The container does NOT clip. It must not: the "⋯" menu is
// absolutely positioned and overflows the box, so any
// `overflow-hidden` here cuts it off (measured: 21 of its 46 px
// hidden, and worse on the last row). Clipping is not needed for
// the collapse either — the session list is not rendered while
// collapsed, and the list's own padding keeps the inner hover
// backgrounds off the rounded corners. It was purely cosmetic and
// it broke the menu.
export function ProjectGroup({ p }: { p: ProjectInfo }) {
  const { lists } = useApp();
  const isCollapsed = lists.collapsedProjects.has(p.id);
  const projectSessions = lists.sessions.filter((s) => s.project_id === p.id);
  return (
    <div class="mt-1.5 rounded-xl bg-white/2 border border-white/6">
      <ProjectHeader p={p} collapsed={isCollapsed} />
      {/* Session list: always rendered, so its height can
          animate. Unmounting it would make the collapse snap,
          which is exactly what was reported. The wrapper
          collapses to `0fr` and `visibility` takes the rows out
          of the tab order and the hit-testing once it has. */}
      <div
        class={`project-sessions${choose(isCollapsed, " is-collapsed", "")}`}
      >
        <div class="project-sessions-inner">
          <div class="px-1.5 pb-1.5 space-y-0.5">
            {projectSessions.map((s) => (
              <SessionRow key={s.id} s={s} />
            ))}
            {projectSessions.length === 0 && (
              <div class="px-3 py-1.5 text-xs text-[#7a7a8c] italic">
                {t("No sessions yet")}
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
