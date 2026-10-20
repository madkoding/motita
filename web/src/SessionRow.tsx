// One session row of the sidebar. On desktop the action buttons appear on hover; on mobile a long
// press opens a context menu with Rename / Delete.
import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { BodyPortal } from "./BodyPortal";
import { useLongPress, useStopClicks } from "./domHooks";
import {
  formatUpdated,
  rowMenuStyle,
  sessionTitle,
  sessionTooltip,
  shortID,
} from "./appFormat";
import { IconCheck, IconClose, IconDots, IconEdit, IconTrash } from "./icons";
import type { SessionInfo } from "./appTypes";
import { SESSIONS_PATH } from "./appConst";

// The look of a menu item, by what it does.
const ITEM = "row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm ";
const ITEM_PLAIN = ITEM + "text-[#e8e8ea] hover:bg-white/5";
const ITEM_DANGER = ITEM + "text-danger hover:bg-danger/10";

// RowMenu is the "⋯" menu of a row, drawn fixed against the viewport so the list's scroll cannot
// clip it. It is portaled to <body>: the sidebar's backdrop-filter makes it the containing block of
// any `fixed` descendant, so inside the sidebar the viewport coordinates were read against the
// sidebar's box and the menu landed anywhere. Its clicks stay inside it.
export function RowMenu({ children }: { children: ComponentChildren }) {
  const { rowMenuAbove, rowMenuClosing, rowMenuPos } = useApp().menus;
  const ref = useRef<HTMLDivElement>(null);
  useStopClicks(ref);
  return (
    <BodyPortal>
      <div
        ref={ref}
        class={`row-menu fixed z-50 frosted rounded-xl border border-white/10 py-1 min-w-[140px]${choose(rowMenuAbove, " row-menu-above", "")}${choose(rowMenuClosing, " row-menu-closing", "")}`}
        style={rowMenuStyle(rowMenuPos)}
      >
        {children}
      </div>
    </BodyPortal>
  );
}

// DeleteItem is the menu item that asks to delete a session or a project.
export function DeleteItem({
  type,
  id,
  title,
}: {
  type: "session" | "project";
  id: string;
  title: string;
}) {
  const { deletion, menus } = useApp();
  return (
    <button
      class={ITEM_DANGER}
      onClick={(e) => {
        e.stopPropagation();
        deletion.setConfirmDelete({ type, id, title });
        menus.closeRowMenu();
      }}
    >
      <IconTrash size={14} />
      {t("Delete")}
    </button>
  );
}

// MergedItems are the items of a session whose work was merged: continue it in a new session,
// and the commit it became.
function MergedItems({ s }: { s: SessionInfo }) {
  const { menus, actions } = useApp();
  return (
    <>
      {s.continuable && (
        <button
          class="row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm text-accent hover:bg-accent/10"
          title={t(
            "Create a new session from the project's current branch and keep working",
          )}
          onClick={async (e) => {
            e.stopPropagation();
            menus.closeRowMenu();
            await actions.continueSession(s);
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path d="M5 12h14" />
            <path d="M12 5v14" />
          </svg>
          {t("Continue")}
        </button>
      )}
      {s.merged_sha && (
        <div class="row-menu-item w-full px-3 py-2 text-xs text-[#9a9aaa]">
          {tf("merged {sha}", { sha: shortID(s.merged_sha) })}
        </div>
      )}
    </>
  );
}

// IsolateItem moves a thread that still works in the project's folder into a worktree of its own.
function IsolateItem({ s }: { s: SessionInfo }) {
  const { menus, actions } = useApp();
  return (
    <button
      class={ITEM_PLAIN}
      title={t(
        "Moves this thread to a worktree of its own, so it stops sharing the project's folder.",
      )}
      onClick={async (e) => {
        e.stopPropagation();
        menus.closeRowMenu();
        await actions.isolateSession(s);
      }}
    >
      {t("Isolate in a worktree")}
    </button>
  );
}

// IntegrateItem is the item that merges a session's work back into its project.
function IntegrateItem({ s }: { s: SessionInfo }) {
  const { menus, stream } = useApp();
  return (
    <button
      class={`row-menu-item w-full flex items-center gap-2 px-3 py-2 text-sm ${choose(s.mergeable, "text-accent hover:bg-accent/10", "text-[#8a8a9a]")}`}
      disabled={!s.mergeable}
      title={choose(
        s.mergeable,
        t("Integrate this session's work back into the project"),
        t("Nothing to integrate yet"),
      )}
      onClick={async (e) => {
        if (!s.mergeable) {
          e.stopPropagation();
          return;
        }
        e.stopPropagation();
        menus.closeRowMenu();
        // Integration is now a run of the agent: it commits, merges and reports through
        // the same SSE channel as a normal task, so the chat shows what happened.
        await stream.submit("Integrate", SESSIONS_PATH + s.id + "/merge");
      }}
    >
      <svg
        width="14"
        height="14"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        stroke-width="2"
        stroke-linecap="round"
        stroke-linejoin="round"
      >
        <path d="M6 3v12" />
        <circle cx="6" cy="18" r="3" />
        <path d="M18 21v-12" />
        <circle cx="18" cy="6" r="3" />
        <path d="M6 9a9 9 0 0 0 12 6" />
      </svg>
      {t("Integrate")}
    </button>
  );
}

// SessionMenu is the "⋯" menu of a session row.
function SessionMenu({ s }: { s: SessionInfo }) {
  const { menus, actions } = useApp();
  return (
    <RowMenu>
      <button
        class={ITEM_PLAIN}
        onClick={(e) => {
          e.stopPropagation();
          actions.startRename(s.id, s.title || "");
          menus.closeRowMenu();
        }}
      >
        <IconEdit size={14} />
        {t("Rename")}
      </button>
      {s.parent_id && !s.worktree && !s.merged && <IsolateItem s={s} />}
      {s.project_id && !s.merged && <IntegrateItem s={s} />}
      {s.project_id && s.merged && <MergedItems s={s} />}
      <DeleteItem type="session" id={s.id} title={s.title || s.id} />
    </RowMenu>
  );
}

// SessionActions is the "⋯" button of a session row, and its menu while it is open.
function SessionActions({ s }: { s: SessionInfo }) {
  const { menus } = useApp();
  const open = menus.rowMenu?.id === s.id;
  return (
    <div
      class={`row-actions relative flex-none${choose(open, " row-actions-open", "")}`}
    >
      <button
        class="p-1 rounded-sm hover:bg-white/10"
        title={t("More actions")}
        data-row-menu-trigger
        onClick={(e) => {
          e.stopPropagation();
          menus.toggleRowMenu(e.currentTarget as HTMLElement, {
            type: "session",
            id: s.id,
            title: s.title || s.id,
          });
        }}
      >
        <IconDots size={16} />
      </button>
      {open && <SessionMenu s={s} />}
    </div>
  );
}

// RenameField is the row while its title is being edited.
function RenameField({ s }: { s: SessionInfo }) {
  const { renameValue, setRenameValue, renameSession, setRenamingId } =
    useApp().actions;
  return (
    <>
      <input
        class="flex-1 min-w-0 bg-black/30 border border-accent/30 rounded-sm px-2 py-1 text-sm text-[#e8e8ea] focus:outline-hidden focus:border-accent"
        value={renameValue}
        autoFocus
        onInput={(e) => setRenameValue((e.target as HTMLInputElement).value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            renameSession(s.id, renameValue);
            setRenamingId(null);
          }
          if (e.key === "Escape") {
            setRenamingId(null);
          }
        }}
        onClick={(e) => e.stopPropagation()}
      />
      <button
        class="p-1 rounded-sm hover:bg-accent/20 text-accent flex-none"
        title={t("Confirm")}
        onClick={(e) => {
          e.stopPropagation();
          renameSession(s.id, renameValue);
          setRenamingId(null);
        }}
      >
        <IconCheck size={14} />
      </button>
      <button
        class="p-1 rounded-sm hover:bg-white/10 text-[#9a9aaa] flex-none"
        title={t("Cancel")}
        onClick={(e) => {
          e.stopPropagation();
          setRenamingId(null);
        }}
      >
        <IconClose size={14} />
      </button>
    </>
  );
}

// SessionFacts is the facts line: every value carries its LABEL and its own colour, so
// a number is never just a number. `main` alone does not say it is
// a branch, and a bare `3` does not say 3 of what. Colour separates
// the kinds at a glance; the label says what each one is on the
// first read, which is what makes colour optional rather than
// load-bearing.
function SessionFacts({ s }: { s: SessionInfo }) {
  return (
    <div class="session-facts flex items-center gap-x-1.5 gap-y-1 flex-wrap mt-1 text-[10px]">
      {!!s.changes && (
        <span class="inline-flex items-center gap-1 flex-none px-1 py-px rounded-sm bg-[#f0a040]/10">
          <span class="text-[#f0a040]/50 uppercase tracking-wide text-[9px]">
            {choose(s.changes === 1, t("change"), t("changes"))}
          </span>
          <span class="tabular-nums text-[#f0a040]">{s.changes}</span>
        </span>
      )}
    </div>
  );
}

// SessionSummary is the row's text: two lines, so the title keeps the full width and the facts
// sit under it instead of competing for the same row: at a narrow sidebar the branch used to
// squeeze the title away.
function SessionSummary({ s, busy }: { s: SessionInfo; busy: boolean }) {
  const agentsRunning = s.agents_running ?? 0;
  return (
    <div class="flex-1 min-w-0">
      <div
        class={`truncate text-sm leading-tight ${choose(busy, "text-accent", "text-[#e8e8ea]")}`}
      >
        {sessionTitle(s)}
      </div>
      {/* When it was last used, on its own line. It was sharing the row
          with the branch and the counts, and at a narrow width the three
          wrapped into a paragraph nobody could read: a timestamp and a
          set of labelled facts are two different kinds of information and
          each gets its own line.
          Its full date, like every label's meaning, is the tooltip of the row's button (see
          SessionRow). */}
      <div class="session-when mt-0.5 text-[10px] tabular-nums text-[#9a9aaa]">
        {formatUpdated(s.last_used)}
        {agentsRunning > 0 && (
          <span class="ml-1.5 text-[#79c0ff]">⧉ {agentsRunning}</span>
        )}
      </div>
      <SessionFacts s={s} />
    </div>
  );
}

// SessionRow draws one session row in the sidebar. The project-grouped layout and the
// free-standing list share it.
export function SessionRow({ s }: { s: SessionInfo }) {
  const { rs, lists, actions, conv, menus } = useApp();
  const rowRef = useRef<HTMLDivElement>(null);
  useLongPress(rowRef, (x, y) =>
    menus.setContextMenu({
      type: "session",
      id: s.id,
      title: s.title || s.id,
      x,
      y,
    }),
  );
  const renaming = actions.renamingId === s.id;
  // `s.running` is the gateway's own answer about this conversation. The
  // local run is folded into the LIST at the two moments this tab knows
  // something the gateway has not said yet (see setRunningState), so a row
  // never has to reason about which client started what - and a run started
  // in another tab, by a scheduled task, or restored after a gateway restart
  // shows up through exactly the same field.
  //
  // `answeredHere` masks the one case where the gateway and the user
  // disagree: the answer has been delivered to this tab but the turn's
  // goroutine has not returned, so the flag is still true for a moment. See
  // answeredRef.
  const busy = s.running === true && !lists.answeredHere(s.id);
  return (
    <div
      ref={rowRef}
      class={`session-row group flex items-center gap-2 px-3 py-2 rounded-lg cursor-pointer transition-colors mb-0.5${choose(
        s.parent_id,
        " session-thread ml-4",
        "",
      )} ${choose(
        s.id === rs.sessionId,
        "bg-accent/10 border border-accent/20",
        "hover:bg-white/5 border border-transparent",
      )}`}
    >
      {/* The activity spinner. It is ALWAYS mounted and absolutely positioned
          against the row (`.session-spinner`, index.css), so it comes and goes
          without moving the title, the timestamp or the facts by a pixel: the
          in-flow version pushed the row's whole content sideways for as long as
          a run lasted. The class carries the state; the transition and the
          rotation are plain CSS, because this build disables Tailwind's
          `animation` plugin and an `animate-spin` icon never turned at all. */}
      <span
        class={`session-spinner${choose(busy, " is-running", "")}`}
        aria-hidden="true"
      />
      {renaming && <RenameField s={s} />}
      {!renaming && <SessionSummary s={s} busy={busy} />}
      {/* The row opens its conversation from anywhere on it: this button covers the row. It comes
          AFTER the text and BEFORE the actions on purpose: positioned boxes and stacking contexts
          (opacity, transform...) are painted in document order, so whatever the text's styles
          become it stays under the button, and the "⋯" stays above it. As the text is under it,
          the button carries the text's tooltips (sessionTooltip). */}
      {!renaming && (
        <button
          type="button"
          class="row-hit"
          aria-label={sessionTitle(s)}
          title={sessionTooltip(s)}
          onClick={() => conv.switchSession(s.id)}
        />
      )}
      {!renaming && <SessionActions s={s} />}
    </div>
  );
}
