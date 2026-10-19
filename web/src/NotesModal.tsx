// The notes window: a vault of plain Markdown notes with a folder tree, an editor with a preview
// where [[links]] open the note they name, the notes that link back, and a search.
import { useMemo, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { Markdown } from "./Markdown";
import { IconClose } from "./icons";
import {
  buildTree,
  layoutGraph,
  parseNoteHref,
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

// NoteButton is one note of the list; the open one is marked.
function NoteButton({ n, label }: { n: NoteItem; label?: string }) {
  const { current, openNote } = useApp().notes;
  const open = current?.path === n.path;
  return (
    <button
      class={`w-full text-left px-2.5 min-h-[36px] rounded-lg text-sm truncate ${choose(open, "bg-accent/15 text-accent", "text-[#e8e8ea] hover:bg-white/5")}`}
      aria-current={open ? "true" : undefined}
      data-testid="note-row"
      onClick={() => void openNote(n.path)}
    >
      {label ?? n.title}
    </button>
  );
}

// FolderNode is a folder of the tree: its sub-folders and its notes, collapsible.
function FolderNode({ folder, top }: { folder: NoteFolder; top?: boolean }) {
  const content = (
    <>
      {folder.folders.map((f) => (
        <FolderNode key={f.path} folder={f} />
      ))}
      {folder.notes.map((n) => (
        <NoteButton key={n.path} n={n} />
      ))}
    </>
  );
  if (top) {
    return content;
  }
  return (
    <details open class="note-folder" data-testid="note-folder">
      <summary class="px-2.5 min-h-[34px] flex items-center text-xs uppercase tracking-wide text-[#9a9aaa] cursor-pointer">
        {folder.name}
      </summary>
      <div class="pl-3 border-l border-white/5 ml-2">{content}</div>
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
        <div class="text-sm text-[#e8e8ea] truncate">{h.path}</div>
        {!!h.snippet && (
          <div class="text-xs text-[#9a9aaa] truncate">{h.snippet}</div>
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
            <FolderNode folder={buildTree(notes.notes)} top />
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

// Backlinks are the notes that link to the open one.
function Backlinks() {
  const { current, openNote } = useApp().notes;
  const back = (current as NoteView).backlinks;
  return (
    <section class="mt-3 pt-3 border-t border-white/5" data-testid="backlinks">
      <h3 class="text-xs uppercase tracking-wide text-[#9a9aaa] mb-1.5">
        {tf("Linked from ({n})", { n: String(back.length) })}
      </h3>
      {back.length === 0 && (
        <p class="text-sm text-[#7a7a8c]">
          {t("No other note links here yet.")}
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
              <div class="text-xs text-[#9a9aaa] truncate">{b.context}</div>
            </button>
          </li>
        ))}
      </ul>
    </section>
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
      class="skill-body artifact-body note-preview relative overflow-auto flex-1 p-3 rounded-lg bg-black/30"
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
      <div class="flex-1 min-w-0 text-sm font-mono text-[#c8c8d2] truncate">
        {current?.path}
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

// GRAPH_W and GRAPH_H are the box the graph is laid out in; the picture scales to the window.
const GRAPH_W = 640;
const GRAPH_H = 440;

// GraphPane draws the notes as dots joined by their links; a click on a dot opens the note.
function GraphPane({ graph }: { graph: NoteGraph }) {
  const { openNote, closeGraph } = useApp().notes;
  const nodes = useMemo(() => layoutGraph(graph, GRAPH_W, GRAPH_H), [graph]);
  const at = new Map(nodes.map((n) => [n.path, n]));
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
      </div>
      {nodes.length === 0 && (
        <p class="text-sm text-[#9a9aaa]">{t("Nothing to draw yet.")}</p>
      )}
      <svg
        viewBox={`0 0 ${GRAPH_W} ${GRAPH_H}`}
        class="flex-1 min-h-0 w-full rounded-lg bg-black/30"
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
            stroke="currentColor"
            stroke-opacity="0.35"
            class="text-accent"
          />
        ))}
        {nodes.map((n) => (
          <g
            key={n.path}
            class="cursor-pointer"
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
              r={5 + Math.min(n.in, 6)}
              class="fill-accent"
            />
            <text
              x={n.x}
              y={n.y - 10 - Math.min(n.in, 6)}
              text-anchor="middle"
              font-size="12"
              class="fill-current text-[#e8e8ea]"
            >
              {n.title}
            </text>
          </g>
        ))}
      </svg>
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
        <div class="m-auto max-w-sm text-center text-sm text-[#9a9aaa] space-y-2">
          <p>{t("Pick a note, or write a new one.")}</p>
          <p>
            {t(
              "Link notes with [[double brackets]]. They are plain .md files: open this folder in Obsidian and they are all there.",
            )}
          </p>
          {!!root && <p class="font-mono text-xs break-all">{root}</p>}
        </div>
      )}
      {!!current && (
        <>
          <NoteToolbar />
          <div class="flex flex-col flex-1 min-h-0 overflow-auto">
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
          <h2 class="text-base font-semibold">{t("Notes")}</h2>
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
            {t("Graph")}
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
