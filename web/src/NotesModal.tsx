// The notes window: a vault of plain Markdown notes with a folder tree, an editor with a preview
// where [[links]] open the note they name, the notes that link back, and a search.
import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { Markdown } from "./Markdown";
import { IconBook, IconClose, IconFile, IconFolder } from "./icons";
import {
  graph3d,
  mountGraph3D,
  NEON,
  reducedMotion,
  type Graph3D,
} from "./noteGraph3d";
import {
  buildTree,
  byReadingOrder,
  layoutGraph,
  noteGroup,
  noteTrail,
  parseNoteHref,
  plainWiki,
  START_NOTE,
  wikiToMarkdown,
  type NoteFolder,
  type NoteGraph,
  type NoteHit,
  type NoteItem,
  type NoteView,
} from "./notesApi";

const FIELD =
  "w-full min-h-[40px] px-3 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#7a7a8c]";
const SMALL_BTN =
  "px-3 min-h-[34px] rounded-lg border border-white/10 text-xs text-[#c8c8d2] hover:bg-white/5 active:scale-95 transition-transform";

// NoteButton is one note of the list; the open one is marked, and the start of a guide has a book.
function NoteButton({ n }: { n: NoteItem }) {
  const { current, openNote } = useApp().notes;
  const open = current?.path === n.path;
  const Icon = choose(n.title === START_NOTE, IconBook, IconFile);
  return (
    <button
      class={`w-full flex items-center gap-2 text-left px-2.5 min-h-[36px] rounded-lg text-sm ${choose(open, "bg-accent/15 text-accent", "text-[#e8e8ea] hover:bg-white/5")}`}
      aria-current={open ? "true" : undefined}
      data-testid="note-row"
      onClick={() => void openNote(n.path)}
    >
      <Icon size={15} class="flex-none opacity-70" />
      <span class="truncate">{n.title}</span>
    </button>
  );
}

// countNotes is how many notes a folder holds, its sub-folders included.
function countNotes(folder: NoteFolder): number {
  return folder.folders.reduce(
    (n, f) => n + countNotes(f),
    folder.notes.length,
  );
}

// FolderNode is a folder of the tree: its sub-folders and its notes, collapsible. The folders at
// the top start open, deeper ones closed unless they hold the open note.
function FolderNode({ folder, depth }: { folder: NoteFolder; depth: number }) {
  const { current } = useApp().notes;
  const content = (
    <>
      {folder.folders.map((f) => (
        <FolderNode key={f.path} folder={f} depth={depth + 1} />
      ))}
      {[...folder.notes].sort(byReadingOrder).map((n) => (
        <NoteButton key={n.path} n={n} />
      ))}
    </>
  );
  if (depth === 0) {
    return content;
  }
  const holdsOpen = !!current?.path.startsWith(folder.path + "/");
  return (
    <details
      open={depth === 1 || holdsOpen}
      class="note-folder"
      data-testid="note-folder"
    >
      <summary class="px-2.5 min-h-[36px] flex items-center gap-2 text-sm text-[#c8c8d2] cursor-pointer rounded-lg hover:bg-white/5">
        <IconFolder size={15} class="flex-none text-accent" />
        <span class="truncate flex-1">{folder.name}</span>
        <span class="text-xs text-[#7a7a8c]">{countNotes(folder)}</span>
      </summary>
      <div class="pl-3 border-l border-white/5 ml-3.5">{content}</div>
    </details>
  );
}

// HitRow is one passage the search found.
function HitRow({ h }: { h: NoteHit }) {
  const { openNote } = useApp().notes;
  return (
    <li>
      <button
        class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-white/5"
        data-testid="note-hit"
        onClick={() => void openNote(h.path)}
      >
        <div class="text-sm text-[#e8e8ea] truncate">{h.title}</div>
        {noteTrail(h.path).length > 0 && (
          <div class="text-xs text-accent/80 truncate">
            {noteTrail(h.path).join(" › ")}
          </div>
        )}
        {!!h.snippet && (
          <div class="text-xs text-[#9a9aaa] truncate">
            {plainWiki(h.snippet)}
          </div>
        )}
      </button>
    </li>
  );
}

// NewNote is the form that makes a note, at the top level or in the folder of a project.
function NewNote() {
  const { lists, notes } = useApp();
  const [title, setTitle] = useState("");
  const [folder, setFolder] = useState("");
  const submit = (e: Event) => {
    e.preventDefault();
    void notes.createNote(folder, title);
    setTitle("");
  };
  return (
    <form class="flex flex-col gap-2" onSubmit={submit}>
      <input
        class={FIELD}
        value={title}
        placeholder={t("New note name")}
        aria-label={t("New note name")}
        data-testid="note-new-name"
        onInput={(e) => setTitle((e.target as HTMLInputElement).value)}
      />
      <div class="flex gap-2">
        {lists.projects.length > 0 && (
          <select
            class={FIELD + " flex-1 min-w-0"}
            value={folder}
            aria-label={t("Folder")}
            data-testid="note-new-folder"
            onChange={(e) => setFolder((e.target as HTMLSelectElement).value)}
          >
            <option value="">{t("No folder")}</option>
            {lists.projects.map((p) => (
              <option key={p.id} value={p.title}>
                {p.title}
              </option>
            ))}
          </select>
        )}
        <button
          type="submit"
          class="flex-none px-4 min-h-[40px] rounded-lg bg-accent text-white text-sm active:scale-95 transition-transform"
          data-testid="note-create"
        >
          {t("Add")}
        </button>
      </div>
    </form>
  );
}

// NoteList is the left side: the search, the new note form and the tree or the search results.
function NoteList() {
  const { notes } = useApp();
  const { current, graph, query, hits, search } = notes;
  return (
    <aside
      class={`${choose(!!current || !!graph, "hidden md:flex", "flex")} flex-col gap-3 w-full md:w-[18rem] flex-none min-h-0 md:border-r md:border-white/5 md:pr-3`}
    >
      <input
        type="search"
        class={FIELD}
        value={query}
        placeholder={t("Search the notes")}
        aria-label={t("Search the notes")}
        data-testid="note-search"
        onInput={(e) => void search((e.target as HTMLInputElement).value)}
      />
      <NewNote />
      <div class="overflow-auto flex-1 min-h-0">
        {query.trim() === "" ? (
          <>
            <FolderNode folder={buildTree(notes.notes)} depth={0} />
            {notes.notes.length === 0 && (
              <p class="text-sm text-[#9a9aaa] px-1">
                {t("No notes yet. Write the first one above.")}
              </p>
            )}
          </>
        ) : (
          <ul>
            {hits.map((h) => (
              <HitRow key={h.path + ":" + h.line} h={h} />
            ))}
            {hits.length === 0 && (
              <p class="text-sm text-[#9a9aaa] px-1">
                {t("Nothing matches that.")}
              </p>
            )}
          </ul>
        )}
      </div>
    </aside>
  );
}

// BACKLINKS_OPEN is how many mentions show open; more start folded so the note stays in view.
const BACKLINKS_OPEN = 5;

// Backlinks are the notes that link to the open one.
function Backlinks() {
  const { current, openNote } = useApp().notes;
  const back = (current as NoteView).backlinks;
  return (
    <details
      class="note-folder mt-3 pt-3 border-t border-white/5"
      open={back.length <= BACKLINKS_OPEN}
      data-testid="backlinks"
    >
      <summary class="flex items-center gap-2 text-xs uppercase tracking-wide text-[#9a9aaa] mb-1.5 cursor-pointer">
        {tf("Mentioned in ({n})", { n: String(back.length) })}
      </summary>
      {back.length === 0 && (
        <p class="text-sm text-[#7a7a8c]">
          {t("No other note mentions this one yet.")}
        </p>
      )}
      <ul class="space-y-1">
        {back.map((b) => (
          <li key={b.path}>
            <button
              class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-white/5"
              data-testid="backlink"
              onClick={() => void openNote(b.path)}
            >
              <div class="text-sm text-accent truncate">{b.title}</div>
              <div class="text-xs text-[#9a9aaa] truncate">
                {plainWiki(b.context)}
              </div>
            </button>
          </li>
        ))}
      </ul>
    </details>
  );
}

// NoteBody is the note as text to edit, or drawn, with its [[links]] alive.
function NoteBody() {
  const { current, draft, setDraft, editing, followLink } = useApp().notes;
  if (editing) {
    return (
      <textarea
        class="flex-1 min-h-[40vh] w-full p-3 rounded-lg bg-black/30 border border-white/10 font-mono text-sm text-[#e8e8ea] resize-none"
        value={draft}
        aria-label={t("Note text")}
        data-testid="note-editor"
        spellcheck={false}
        onInput={(e) => setDraft((e.target as HTMLTextAreaElement).value)}
      />
    );
  }
  const onClick = (e: MouseEvent) => {
    const href = (e.target as Element).closest("a")?.getAttribute("href");
    if (parseNoteHref(href)) {
      e.preventDefault();
      void followLink(href);
    }
  };
  return (
    <div
      class="skill-body artifact-body note-preview relative p-3 md:px-5 rounded-lg bg-black/30"
      data-testid="note-preview"
      onClick={onClick}
    >
      <Markdown content={wikiToMarkdown(draft, (current as NoteView).links)} />
    </div>
  );
}

// NoteToolbar is what can be done with the open note.
function NoteToolbar() {
  const {
    current,
    draft,
    editing,
    setEditing,
    back,
    saveNote,
    renameNote,
    deleteNote,
  } = useApp().notes;
  const [armed, setArmed] = useState(false);
  // The name being typed while renaming; null when the field is closed.
  const [name, setName] = useState<string | null>(null);
  const rename = (e: Event) => {
    e.preventDefault();
    void renameNote(name as string);
    setName(null);
  };
  const dirty = current?.body !== draft;
  const remove = () => {
    if (armed) {
      setArmed(false);
      void deleteNote();
    } else {
      setArmed(true);
    }
  };
  return (
    <div class="flex items-center gap-2 mb-2 flex-wrap">
      <button
        class={SMALL_BTN + " md:hidden"}
        data-testid="note-back"
        onClick={() => void back()}
      >
        {t("Back to the list")}
      </button>
      <div class="flex-1 min-w-0" data-testid="note-place">
        {noteTrail((current as NoteView).path).length > 0 && (
          <div class="text-xs text-[#9a9aaa] truncate">
            {noteTrail((current as NoteView).path).join(" › ")}
          </div>
        )}
        <div class="text-sm font-semibold text-[#e8e8ea] truncate">
          {(current as NoteView).title}
        </div>
      </div>
      <button
        class={SMALL_BTN}
        aria-pressed={editing}
        data-testid="note-mode"
        onClick={() => setEditing(!editing)}
      >
        {choose(editing, t("Preview"), t("Edit"))}
      </button>
      {dirty && (
        <button
          class={SMALL_BTN + " bg-accent text-white border-accent"}
          data-testid="note-save"
          onClick={() => void saveNote()}
        >
          {t("Save")}
        </button>
      )}
      <button
        class={SMALL_BTN}
        aria-pressed={name !== null}
        data-testid="note-rename"
        onClick={() =>
          setName(choose(name === null, (current as NoteView).title, null))
        }
      >
        {t("Rename")}
      </button>
      <button
        class={SMALL_BTN + " text-danger"}
        data-testid="note-delete"
        onClick={remove}
        onBlur={() => setArmed(false)}
      >
        {choose(armed, t("Click again to delete"), t("Delete"))}
      </button>
      {name !== null && (
        <form class="flex gap-2 w-full" onSubmit={rename}>
          <input
            class={FIELD + " flex-1 min-w-0"}
            value={name}
            aria-label={t("New name")}
            data-testid="note-rename-name"
            onInput={(e) => setName((e.target as HTMLInputElement).value)}
          />
          <button type="submit" class={SMALL_BTN} data-testid="note-rename-go">
            {t("Rename")}
          </button>
        </form>
      )}
    </div>
  );
}

// NEON_CSS is the map's palette as CSS colours, one per top folder.
const HEX_DIGITS = 6;
const NEON_CSS = NEON.map(
  (c) => "#" + c.toString(16).padStart(HEX_DIGITS, "0"),
);

// DOT sizes a note on the flat map: its radius grows with the links to it, up to most, and its
// name sits lift above it.
const DOT = { size: 5, most: 6, lift: 10 };

// GRAPH_W and GRAPH_H are the box the graph is laid out in; the picture scales to the window.
const GRAPH_W = 640;
const GRAPH_H = 440;

// useMap3D draws the graph in 3D into host where WebGL works, and answers whether it did; until
// then, or anywhere else, the flat drawing stays.
function useMap3D(graph: NoteGraph, open: (path: string) => void) {
  const host = useRef<HTMLDivElement>(null);
  const map = useRef<Graph3D | null>(null);
  const [deep, setDeep] = useState(false);
  useEffect(() => {
    if (graph.nodes.length === 0 || !graph3d.webgl()) {
      return undefined;
    }
    let live = true;
    graph3d
      .load()
      .then((T) => {
        if (live) {
          map.current = mountGraph3D(
            T,
            host.current as HTMLDivElement,
            graph,
            open,
            reducedMotion(),
          );
          setDeep(true);
        }
      })
      .catch(() => setDeep(false));
    return () => {
      live = false;
      map.current?.stop();
      map.current = null;
    };
  }, [graph]);
  return { host, map, deep };
}

// GraphPane draws the notes as glowing points joined by their links, in 3D where it can; a click
// on a note opens it.
function GraphPane({ graph }: { graph: NoteGraph }) {
  const { openNote, closeGraph } = useApp().notes;
  const nodes = useMemo(() => layoutGraph(graph, GRAPH_W, GRAPH_H), [graph]);
  const at = new Map(nodes.map((n) => [n.path, n]));
  const { host, map, deep } = useMap3D(graph, (p) => void openNote(p));
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const hit = (title: string) => q !== "" && title.toLowerCase().includes(q);
  const found = graph.nodes.filter((n) => hit(n.title)).length;
  useEffect(() => {
    map.current?.find(query);
  }, [query, deep]);
  return (
    <div class="flex flex-col flex-1 min-h-0" data-testid="note-graph">
      <div class="flex items-center gap-2 mb-2 flex-wrap">
        <button
          class={SMALL_BTN + " md:hidden"}
          data-testid="graph-back"
          onClick={closeGraph}
        >
          {t("Back to the list")}
        </button>
        <div class="flex-1 min-w-0 text-xs text-[#9a9aaa]">
          {tf(
            "{notes} notes · {links} links · {missing} to notes not written yet",
            {
              notes: String(graph.nodes.length),
              links: String(graph.edges.length),
              missing: String(graph.missing),
            },
          )}
        </div>
        {deep && (
          <div class="text-xs text-accent" data-testid="graph-3d-hint">
            {t("Drag to turn, scroll to zoom, click a name to open it.")}
          </div>
        )}
      </div>
      {nodes.length > 0 && (
        <div class="flex items-center gap-2 mb-2">
          <input
            type="search"
            class={FIELD + " md:max-w-[18rem]"}
            value={query}
            placeholder={t("Find a note on the map")}
            aria-label={t("Find a note on the map")}
            data-testid="graph-search"
            onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          />
          {q !== "" && (
            <span class="text-xs text-[#f5f500]" data-testid="graph-found">
              {tf("{n} found", { n: String(found) })}
            </span>
          )}
        </div>
      )}
      {nodes.length === 0 && (
        <p class="text-sm text-[#9a9aaa]">{t("Nothing to draw yet.")}</p>
      )}
      <div class="graph3d relative flex-1 min-h-0 rounded-xl overflow-hidden">
        <div ref={host} class="absolute inset-0" data-testid="graph-3d" />
        {!deep && (
          <svg
            viewBox={`0 0 ${GRAPH_W} ${GRAPH_H}`}
            class="relative w-full h-full"
            role="img"
            aria-label={t("Graph of linked notes")}
          >
            {graph.edges.map((e) => (
              <line
                key={e.from + ">" + e.to}
                x1={at.get(e.from)?.x}
                y1={at.get(e.from)?.y}
                x2={at.get(e.to)?.x}
                y2={at.get(e.to)?.y}
                stroke="#00f0ff"
                stroke-opacity="0.4"
              />
            ))}
            {nodes.map((n) => (
              <g
                key={n.path}
                class={
                  "cursor-pointer graph2d-node" +
                  choose(hit(n.title), " is-hit", "") +
                  choose(q !== "" && !hit(n.title), " is-dim", "")
                }
                role="button"
                tabIndex={0}
                data-testid="graph-node"
                aria-label={n.title}
                onClick={() => void openNote(n.path)}
                onKeyDown={(e) => e.key === "Enter" && void openNote(n.path)}
              >
                <circle
                  cx={n.x}
                  cy={n.y}
                  r={DOT.size + Math.min(n.in, DOT.most)}
                  fill={NEON_CSS[noteGroup(n.path, NEON_CSS.length)]}
                />
                <text
                  x={n.x}
                  y={n.y - DOT.lift - Math.min(n.in, DOT.most)}
                  text-anchor="middle"
                  font-size="12"
                  fill="#e8e8ea"
                >
                  {n.title}
                </text>
              </g>
            ))}
          </svg>
        )}
      </div>
    </div>
  );
}

// OpenNote is the right side: the open note, or what to do when none is.
function OpenNote() {
  const { current, graph, root } = useApp().notes;
  return (
    <section
      class={`${choose(!!current || !!graph, "flex", "hidden md:flex")} flex-col flex-1 min-w-0 min-h-0 md:pl-1`}
    >
      {!!graph && <GraphPane graph={graph} />}
      {!current && !graph && (
        <div class="m-auto max-w-sm text-center text-sm text-[#9a9aaa] space-y-3">
          <IconBook size={36} class="mx-auto text-accent opacity-80" />
          <p class="text-base text-[#e8e8ea]">
            {t("Pick a note, or write a new one.")}
          </p>
          <p>
            {t(
              "Each project has a manual: open its folder and start with Start here. Link notes with [[double brackets]].",
            )}
          </p>
          <p>
            {t(
              "They are plain .md files: open this folder in Obsidian and they are all there.",
            )}
          </p>
          {!!root && (
            <p class="text-xs break-all">{tf("Saved in {root}", { root })}</p>
          )}
        </div>
      )}
      {!!current && (
        <>
          <NoteToolbar />
          <div
            key={(current as NoteView).path}
            class="flex flex-col flex-1 min-h-0 overflow-auto"
          >
            <NoteBody />
            <Backlinks />
          </div>
        </>
      )}
    </section>
  );
}

export function NotesModal() {
  const app = useApp();
  const { closeNotes, graph, openGraph, closeGraph } = app.notes;
  const toggleGraph = () => {
    if (graph) {
      closeGraph();
    } else {
      void openGraph();
    }
  };
  return (
    <ModalBackdrop onClose={() => void closeNotes()}>
      <div class="frosted rounded-2xl border border-white/10 w-full max-w-5xl p-4 md:p-5 shadow-2xl h-[85vh] flex flex-col">
        <div class="flex items-center gap-2 mb-3">
          <div class="min-w-0">
            <h2 class="text-base font-semibold">{t("Notes")}</h2>
            <p class="text-xs text-[#9a9aaa] truncate hidden sm:block">
              {t("Your second brain: projects, sessions and your own ideas.")}
            </p>
          </div>
          <button
            class={SMALL_BTN + " ml-auto"}
            title={t(
              "Ask the agent to find contradictions, stale notes and broken links",
            )}
            data-testid="notes-review"
            onClick={() => void app.reviewNotes()}
          >
            {t("Review notes")}
          </button>
          <button
            class={SMALL_BTN}
            aria-pressed={!!graph}
            data-testid="note-graph-toggle"
            onClick={toggleGraph}
          >
            {choose(!!graph, t("Close map"), t("Map"))}
          </button>
          <button
            class="p-1.5 rounded-lg hover:bg-white/5"
            onClick={() => void closeNotes()}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>
        <div class="flex flex-1 min-h-0 gap-3">
          <NoteList />
          <OpenNote />
        </div>
      </div>
    </ModalBackdrop>
  );
}
