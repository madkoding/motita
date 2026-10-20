// The line icons the page draws in more than one place. Each takes the size it is drawn at and,
// where the places differ, the stroke and the class.
import type { ComponentChildren } from "preact";
import { choose } from "./choose";

interface IconProps {
  size: number;
  stroke?: number | string;
  class?: string;
  style?: string;
}

// Line draws an icon from its strokes, in the style every icon of the page shares.
function Line({
  size,
  stroke = "2",
  class: cls,
  style,
  children,
}: IconProps & { children: ComponentChildren }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width={stroke}
      stroke-linecap="round"
      stroke-linejoin="round"
      class={cls}
      style={style}
    >
      {children}
    </svg>
  );
}

// IconClose is the ✕ of a dialog, a toast or a field.
export function IconClose(p: IconProps) {
  return (
    <Line {...p}>
      <line x1="18" y1="6" x2="6" y2="18" />
      <line x1="6" y1="6" x2="18" y2="18" />
    </Line>
  );
}

// IconTrash is the bin of a delete.
export function IconTrash(p: IconProps) {
  return (
    <Line {...p}>
      <polyline points="3 6 5 6 21 6" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
    </Line>
  );
}

// IconEdit is the pencil of a rename.
export function IconEdit(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7" />
      <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z" />
    </Line>
  );
}

// IconSpinner is the arc that turns while something is on its way.
export function IconSpinner(p: IconProps) {
  return (
    <Line {...p} stroke={p.stroke ?? "2.5"}>
      <path d="M21 12a9 9 0 1 1-6.219-8.56" />
    </Line>
  );
}

// IconAlert is the circled ! of an error.
export function IconAlert(p: IconProps) {
  return (
    <Line {...p}>
      <circle cx="12" cy="12" r="10" />
      <line x1="12" y1="8" x2="12" y2="12" />
      <line x1="12" y1="16" x2="12.01" y2="16" />
    </Line>
  );
}

// IconDownload is the arrow into a tray of a new version.
export function IconDownload(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </Line>
  );
}

// IconCheck is the tick of something done.
export function IconCheck(p: IconProps) {
  return (
    <Line {...p} stroke={p.stroke ?? "2.5"}>
      <polyline points="20 6 9 17 4 12" />
    </Line>
  );
}

// IconFolder is a project's folder.
export function IconFolder(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
    </Line>
  );
}

// IconFolderPlus is the folder of a new project.
export function IconFolderPlus(p: IconProps & { hidden?: boolean }) {
  return (
    <svg
      class={p.class}
      width={p.size}
      height={p.size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden={choose(p.hidden, "true", undefined)}
    >
      <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
      <line x1="12" y1="11" x2="12" y2="17" />
      <line x1="9" y1="14" x2="15" y2="14" />
    </svg>
  );
}

// IconMonitor is the screen of the model.
export function IconMonitor(p: IconProps) {
  return (
    <Line {...p}>
      <rect x="2" y="3" width="20" height="14" rx="2" />
      <line x1="8" y1="21" x2="16" y2="21" />
      <line x1="12" y1="17" x2="12" y2="21" />
    </Line>
  );
}

// IconBook is the open book of the skill library.
export function IconBook(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M2 3h6a4 4 0 0 1 4 4v14a3 3 0 0 0-3-3H2z" />
      <path d="M22 3h-6a4 4 0 0 0-4 4v14a3 3 0 0 1 3-3h7z" />
    </Line>
  );
}

// IconNotes is the pencil on a page of the notes.
export function IconNotes(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z" />
    </Line>
  );
}

// IconFile is a page: one note of the notes list.
export function IconFile(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z" />
      <polyline points="14 3 14 8 19 8" />
    </Line>
  );
}

// IconClock is the clock of the scheduled tasks.
export function IconClock(p: IconProps) {
  return (
    <Line {...p}>
      <circle cx="12" cy="12" r="10" />
      <polyline points="12 6 12 12 16 14" />
    </Line>
  );
}

// IconDots is the ⋯ of a row's menu.
export function IconDots(p: IconProps) {
  return (
    <Line {...p}>
      <circle cx="12" cy="5" r="1" />
      <circle cx="12" cy="12" r="1" />
      <circle cx="12" cy="19" r="1" />
    </Line>
  );
}

// IconRefresh is the arrows of an update.
export function IconRefresh(p: IconProps) {
  return (
    <Line {...p}>
      <polyline points="23 4 23 10 17 10" />
      <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
    </Line>
  );
}

// IconArrowDown is what comes in from origin.
export function IconArrowDown(p: IconProps) {
  return (
    <Line {...p}>
      <line x1="12" y1="5" x2="12" y2="19" />
      <polyline points="19 12 12 19 5 12" />
    </Line>
  );
}

// IconArrowUp is what goes up to origin.
export function IconArrowUp(p: IconProps) {
  return (
    <Line {...p}>
      <line x1="12" y1="19" x2="12" y2="5" />
      <polyline points="5 12 12 5 19 12" />
    </Line>
  );
}

// IconBack is going back to the main branch.
export function IconBack(p: IconProps) {
  return (
    <Line {...p}>
      <polyline points="9 14 4 9 9 4" />
      <path d="M20 20v-7a4 4 0 0 0-4-4H4" />
    </Line>
  );
}

// IconArchive is the changes put aside.
export function IconArchive(p: IconProps) {
  return (
    <Line {...p}>
      <polyline points="21 8 21 21 3 21 3 8" />
      <rect x="1" y="3" width="22" height="5" />
      <line x1="10" y1="12" x2="14" y2="12" />
    </Line>
  );
}

// IconBranch is a branch.
export function IconBranch(p: IconProps) {
  return (
    <Line {...p}>
      <line x1="6" y1="3" x2="6" y2="15" />
      <circle cx="18" cy="6" r="3" />
      <circle cx="6" cy="18" r="3" />
      <path d="M18 9a9 9 0 0 1-9 9" />
    </Line>
  );
}

// IconCommit is a commit.
export function IconCommit(p: IconProps) {
  return (
    <Line {...p}>
      <circle cx="12" cy="12" r="4" />
      <line x1="1.05" y1="12" x2="7" y2="12" />
      <line x1="17.01" y1="12" x2="22.96" y2="12" />
    </Line>
  );
}

// IconPullRequest is a pull request.
export function IconPullRequest(p: IconProps) {
  return (
    <Line {...p}>
      <circle cx="18" cy="18" r="3" />
      <circle cx="6" cy="6" r="3" />
      <path d="M13 6h3a2 2 0 0 1 2 2v7" />
      <line x1="6" y1="9" x2="6" y2="21" />
    </Line>
  );
}

// IconEye is looking at what a repository holds.
export function IconEye(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
      <circle cx="12" cy="12" r="3" />
    </Line>
  );
}

// IconChat is a request to the agent.
export function IconChat(p: IconProps) {
  return (
    <Line {...p}>
      <path d="M21 11.5a8.38 8.38 0 0 1-.9 3.8 8.5 8.5 0 0 1-7.6 4.7 8.38 8.38 0 0 1-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 0 1-.9-3.8 8.5 8.5 0 0 1 4.7-7.6 8.38 8.38 0 0 1 3.8-.9h.5a8.48 8.48 0 0 1 8 8v.5z" />
    </Line>
  );
}
