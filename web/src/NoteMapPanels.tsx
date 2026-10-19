// What sits over the map of the notes: the legend of its colours, and a look at a note beside it.
import { useEffect, useRef } from "preact/hooks";
import { t } from "./i18n";
import { choose } from "./choose";
import { Markdown } from "./Markdown";
import { IconClose } from "./icons";
import { NEON_CSS } from "./noteGraph3d";
import {
  mapGroups,
  noteTrail,
  parseNoteHref,
  wikiToMarkdown,
  type GraphNode,
  type NoteView,
} from "./notesApi";

// LEGEND_MAX is how many parts the legend names; the smaller ones keep their colour unnamed.
const LEGEND_MAX = 8;

// MapLegend names the colour of each part of the vault, the biggest first.
export function MapLegend({ nodes }: { nodes: GraphNode[] }) {
  const groups = mapGroups(nodes).slice(0, LEGEND_MAX);
  return (
    <ul
      class="graph-legend absolute left-2 bottom-2 z-10 flex flex-wrap gap-1.5 max-w-[70%] pointer-events-none"
      aria-label={t("Colours of the map")}
      data-testid="graph-legend"
    >
      {groups.map((g, i) => (
        <li
          key={g.key}
          class="graph-legend-item flex items-center gap-1.5 px-2 py-0.5 rounded-full text-[11px]"
        >
          <span
            class="w-2 h-2 rounded-full flex-none"
            style={{
              background: NEON_CSS[i % NEON_CSS.length],
            }}
          />
          <span class="truncate">
            {choose(
              g.key === "",
              t("Loose notes"),
              g.key.slice(g.key.lastIndexOf("/") + 1),
            )}
          </span>
          <span class="opacity-60">{g.count}</span>
        </li>
      ))}
    </ul>
  );
}

// MapPeek shows a note beside the map without leaving it: its links lead to the next note to look
// at, and a button opens it for good.
export function MapPeek({
  note,
  onPeek,
  onOpen,
  onClose,
}: {
  note: NoteView;
  onPeek: (path: string) => void;
  onOpen: (path: string) => void;
  onClose: () => void;
}) {
  const body = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = body.current as HTMLDivElement;
    const follow = (e: MouseEvent) => {
      const to = parseNoteHref(
        (e.target as Element).closest("a")?.getAttribute("href"),
      );
      if (!to) {
        return;
      }
      e.preventDefault();
      if (to.kind === "note") {
        onPeek(to.path);
      }
    };
    el.addEventListener("click", follow);
    return () => el.removeEventListener("click", follow);
  }, [onPeek]);
  const trail = noteTrail(note.path);
  const open = () => onOpen(note.path);
  return (
    <aside
      class="graph-peek absolute top-2 right-2 bottom-2 z-20 w-[min(22rem,calc(100%-1rem))] flex flex-col rounded-lg"
      aria-label={note.title}
      data-testid="graph-peek"
    >
      <div class="flex items-start gap-2 p-3 border-b border-white/10">
        <div class="min-w-0 flex-1">
          {trail.length > 0 && (
            <div class="text-[11px] text-[#9a9aaa] truncate">
              {trail.join(" › ")}
            </div>
          )}
          <div class="text-sm font-semibold text-[#e8e8ea] truncate">
            {note.title}
          </div>
        </div>
        <button
          type="button"
          class="p-1 rounded text-[#9a9aaa] hover:text-[#e8e8ea]"
          aria-label={t("Close the note")}
          data-testid="graph-peek-close"
          onClick={onClose}
        >
          <IconClose size={16} />
        </button>
      </div>
      <div
        ref={body}
        class="skill-body artifact-body relative flex-1 min-h-0 overflow-auto p-3 text-sm"
        data-testid="graph-peek-body"
      >
        <Markdown content={wikiToMarkdown(note.body, note.links)} />
      </div>
      <div class="p-3 border-t border-white/10">
        <button
          type="button"
          class="w-full min-h-[36px] rounded-lg bg-accent/20 text-accent text-sm hover:bg-accent/30"
          data-testid="graph-peek-open"
          onClick={open}
        >
          {t("Open the note")}
        </button>
      </div>
    </aside>
  );
}
