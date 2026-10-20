// The left side of the notes window, read as a library and not as a file tree: the guides of the
// projects as books with their chapters, the conversations, and the person's own notes, each
// folder with a "⋯" menu to update or delete it.
import { useEffect, useState } from "preact/hooks";
import { plural, t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { DotsMenu, type MenuItem } from "./NoteMenus";
import { IconBook, IconFile, IconFolder, IconNotes } from "./icons";
import { progressShare, progressText } from "./NotesScanSettings";
import {
  ARCHITECTURE_SUFFIX,
  buildTree,
  byReadingOrder,
  CHAPTER_SEP,
  chapterOutline,
  countNotes,
  folderOf,
  noteTrail,
  plainWiki,
  readableName,
  START_NOTE,
  type NoteFolder,
  type NoteHit,
  type NoteItem,
  type ScanFolder,
} from "./notesApi";

const FIELD =
  "w-full min-h-[40px] px-3 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#7a7a8c]";
export const SMALL_BTN =
  "px-3 min-h-[34px] rounded-lg border border-white/10 text-xs text-[#c8c8d2] hover:bg-white/5 active:scale-95 transition-transform";
const ROW =
  "w-full flex items-center gap-2 text-left px-2.5 min-h-[36px] rounded-lg text-sm ";

// folderLabel is how a folder is called: the ones a scan writes by what they hold, any other by
// its name.
export function folderLabel(
  name: string,
  info: ScanFolder | undefined,
): string {
  switch (info?.kind) {
    case "guide":
      return info.title ?? name;
    case "projects":
      return t("Projects");
    case "sessions":
      return t("Conversations");
    default:
      return name;
  }
}

// placeOf says where a note is, as the folders it sits in by the names the person sees.
export function placeOf(path: string, folders: ScanFolder[]): string {
  const trail = noteTrail(path);
  return trail
    .map((name, i) => {
      const at = trail.slice(0, i + 1).join("/");
      return folderLabel(
        name,
        folders.find((f) => f.path === at),
      );
    })
    .join(" › ");
}

// readTitle is what a note is called to the person: a chapter of a guide by the part of the project
// it explains, the notes written about a project by what they are for, any other by its name.
export function readTitle(
  n: { path: string; title: string },
  folders: ScanFolder[],
): string {
  if (n.title === START_NOTE) {
    return t("Start here");
  }
  if (
    folders.some((f) => f.kind === "guide" && n.path.startsWith(f.path + "/"))
  ) {
    return readableName(n.title.split(CHAPTER_SEP).at(-1) as string);
  }
  const home = folders.find((f) => f.path === folderOf(n.path));
  if (home?.kind !== "projects") {
    return n.title;
  }
  if (n.title.endsWith(ARCHITECTURE_SUFFIX)) {
    return tf("Technical overview of {name}", {
      name: n.title.slice(0, -ARCHITECTURE_SUFFIX.length),
    });
  }
  return tf("Project page: {name}", { name: n.title });
}

// NoteButton is one note of the list; the open one is marked, and the start of a guide has a book.
function NoteButton({ n, label }: { n: NoteItem; label: string }) {
  const { current, openNote } = useApp().notes;
  const open = current?.path === n.path;
  const Icon = choose(n.title === START_NOTE, IconBook, IconFile);
  return (
    <button
      class={
        ROW +
        choose(
          open,
          "bg-accent/15 text-accent",
          "text-[#e8e8ea] hover:bg-white/5",
        )
      }
      aria-current={choose(open, "true", undefined)}
      data-testid="note-row"
      onClick={() => void openNote(n.path)}
    >
      <Icon size={15} class="flex-none opacity-70" />
      <span class="truncate">{label}</span>
    </button>
  );
}

// FolderMenu is the "⋯" menu of a folder: write it again when a scan wrote it, and delete it.
function FolderMenu({
  folder,
  info,
  name,
}: {
  folder: NoteFolder;
  info: ScanFolder | undefined;
  name: string;
}) {
  const { rescanFolder, askDelete } = useApp().notes;
  const items: MenuItem[] = [];
  if (info) {
    items.push({
      label: choose(
        info.kind === "guide",
        t("Update this guide"),
        t("Update these notes"),
      ),
      hint: t("Writes them again from your project, in the background"),
      testid: "folder-rescan",
      onSelect: () => void rescanFolder(folder.path),
    });
  }
  items.push({
    label: choose(
      info?.kind === "guide",
      t("Delete this guide"),
      t("Delete this folder"),
    ),
    hint: plural(
      countNotes(folder),
      "Deletes its 1 note",
      "Deletes its {n} notes",
    ),
    danger: true,
    testid: "folder-delete",
    onSelect: () =>
      askDelete({
        kind: "folder",
        path: folder.path,
        name,
        count: countNotes(folder),
        rewritable: !!info,
      }),
  });
  return (
    <DotsMenu
      label={tf("Options for {name}", { name })}
      testid="folder-menu"
      items={items}
    />
  );
}

// holds says whether the open note is in the folder.
function holds(path: string | undefined, folder: NoteFolder): boolean {
  return !!path?.startsWith(folder.path + "/");
}

// useOpen keeps a folder open or closed as the person left it, and opens it when the open note
// moves into it: going back to the list must not fold what was being read.
function useOpen(opens: boolean) {
  const [open, setOpen] = useState(opens);
  useEffect(() => {
    if (opens) {
      setOpen(true);
    }
  }, [opens]);
  return {
    open,
    onToggle: (e: Event) =>
      setOpen((e.currentTarget as HTMLDetailsElement).open),
  };
}

// GuideBook is the guide of a project: a book that opens onto its chapters, each one a level deeper
// than the part of the project it sits in.
function GuideBook({ folder, info }: { folder: NoteFolder; info: ScanFolder }) {
  const { current } = useApp().notes;
  const name = folderLabel(folder.name, info);
  const book = useOpen(holds(current?.path, folder));
  return (
    <details
      open={book.open}
      onToggle={book.onToggle}
      class="note-folder"
      data-testid="note-guide"
    >
      <summary class="px-2.5 min-h-[44px] flex items-center gap-2 text-sm text-[#e8e8ea] cursor-pointer rounded-lg hover:bg-white/5">
        <IconBook size={18} class="flex-none text-accent" />
        <span class="min-w-0 flex-1">
          <span class="block truncate font-medium">{name}</span>
          <span class="block truncate text-xs text-[#9a9aaa]">
            {tf("Guide · {pages}", {
              pages: plural(countNotes(folder), "1 page", "{n} pages"),
            })}
          </span>
        </span>
        <FolderMenu folder={folder} info={info} name={name} />
      </summary>
      <div class="pb-1">
        {chapterOutline(folder.notes).map(({ note, depth, label }) => (
          <div
            key={note.path}
            data-testid="note-chapter"
            style={{ paddingLeft: depth * 12 + 20 + "px" }}
          >
            <NoteButton
              n={note}
              label={choose(note.title === START_NOTE, t("Start here"), label)}
            />
          </div>
        ))}
        {folder.folders.map((f) => (
          <FolderNode key={f.path} folder={f} depth={3} />
        ))}
      </div>
    </details>
  );
}

// FolderNode is a folder of the list with its sub-folders and notes, collapsible. The folders at
// the top start open (the conversations, which are many, start closed), deeper ones closed unless
// they hold the open note.
function FolderNode({ folder, depth }: { folder: NoteFolder; depth: number }) {
  const { folders } = useApp().notes;
  const info = folders.find((f) => f.path === folder.path);
  if (info?.kind === "guide") {
    return <GuideBook folder={folder} info={info} />;
  }
  return <PlainFolder folder={folder} info={info} depth={depth} />;
}

// PlainFolder is a folder that is not a guide.
function PlainFolder({
  folder,
  info,
  depth,
}: {
  folder: NoteFolder;
  info: ScanFolder | undefined;
  depth: number;
}) {
  const { current } = useApp().notes;
  const name = folderLabel(folder.name, info);
  const state = useOpen(
    holds(current?.path, folder) || (depth === 1 && info?.kind !== "sessions"),
  );
  return (
    <details
      open={state.open}
      onToggle={state.onToggle}
      class="note-folder"
      data-testid="note-folder"
    >
      <summary class="px-2.5 min-h-[40px] flex items-center gap-2 text-sm text-[#c8c8d2] cursor-pointer rounded-lg hover:bg-white/5">
        <IconFolder size={15} class="flex-none text-accent" />
        <span class="truncate flex-1">{name}</span>
        <span class="text-xs text-[#7a7a8c]">{countNotes(folder)}</span>
        <FolderMenu folder={folder} info={info} name={name} />
      </summary>
      <div class="pl-3 border-l border-white/5 ml-3.5">
        <FolderContent folder={folder} depth={depth} />
      </div>
    </details>
  );
}

// FolderContent lists a folder's sub-folders, then its notes.
function FolderContent({
  folder,
  depth,
}: {
  folder: NoteFolder;
  depth: number;
}) {
  const { folders } = useApp().notes;
  return (
    <>
      {folder.folders.map((f) => (
        <FolderNode key={f.path} folder={f} depth={depth + 1} />
      ))}
      {[...folder.notes].sort(byReadingOrder).map((n) => (
        <NoteButton key={n.path} n={n} label={readTitle(n, folders)} />
      ))}
    </>
  );
}

// HitRow is one passage the search found.
function HitRow({ h }: { h: NoteHit }) {
  const { openNote, folders } = useApp().notes;
  const place = placeOf(h.path, folders);
  return (
    <li>
      <button
        class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-white/5"
        data-testid="note-hit"
        onClick={() => void openNote(h.path)}
      >
        <div class="text-sm text-[#e8e8ea] truncate">
          {readTitle(h, folders)}
        </div>
        {place !== "" && (
          <div class="text-xs text-accent/80 truncate">{place}</div>
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
function NewNote({ done }: { done: () => void }) {
  const { lists, notes } = useApp();
  const [title, setTitle] = useState("");
  const [folder, setFolder] = useState("");
  const submit = (e: Event) => {
    e.preventDefault();
    void notes.createNote(folder, title);
    setTitle("");
    done();
  };
  return (
    <form class="flex flex-col gap-2" onSubmit={submit}>
      <input
        class={FIELD}
        value={title}
        placeholder={t("What is the note about?")}
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

// Updating says a scan is writing the guides, and how far it got.
function Updating() {
  const { updating } = useApp().notes;
  if (!updating) {
    return null;
  }
  return (
    <output
      class="block px-1 py-1.5 rounded-lg"
      data-testid="notes-updating"
      aria-live="polite"
    >
      <span class="block text-xs text-[#c8c8d2]">
        {t("Updating the guides")}
        {" · "}
        {progressText(updating)}
      </span>
      <span class="block mt-1 min-h-[4px] rounded-full bg-white/10 overflow-hidden">
        <span
          class="block min-h-[4px] bg-accent transition-[width] duration-500"
          style={{ width: progressShare(updating) + "%" }}
        />
      </span>
    </output>
  );
}

// LibraryMenu is what can be done with all the notes at once.
function LibraryMenu() {
  const app = useApp();
  const { rescanFolder } = app.notes;
  return (
    <DotsMenu
      label={t("More options for the notes")}
      testid="library-menu"
      items={[
        {
          label: t("Update the guides"),
          hint: t("Writes the guide of every project again"),
          testid: "library-update",
          onSelect: () => void rescanFolder("Projects"),
        },
        {
          label: t("Review the notes"),
          hint: t(
            "Ask the agent to find contradictions, stale notes and broken links",
          ),
          testid: "notes-review",
          onSelect: () => void app.reviewNotes(),
        },
      ]}
    />
  );
}

// NoteList is the left side: the search, the new note form and the library or the search results.
export function NoteList() {
  const { notes } = useApp();
  const { current, graph, query, hits, search } = notes;
  const [writing, setWriting] = useState(false);
  const closeForm = () => setWriting(false);
  const toggleForm = () => setWriting(!writing);
  const searching = query.trim() !== "";
  return (
    <aside
      class={`${choose(!!current || !!graph, "hidden md:flex", "flex")} flex-col gap-3 w-full md:w-[19rem] flex-none min-h-0 md:border-r md:border-white/5 md:pr-3`}
    >
      <div class="flex items-center gap-2">
        <input
          type="search"
          class={FIELD}
          value={query}
          placeholder={t("Search the notes")}
          aria-label={t("Search the notes")}
          data-testid="note-search"
          onInput={(e) => void search((e.target as HTMLInputElement).value)}
        />
        <LibraryMenu />
      </div>
      <button
        class={SMALL_BTN + " flex items-center justify-center gap-2"}
        aria-expanded={writing}
        data-testid="note-new-toggle"
        onClick={toggleForm}
      >
        <IconNotes size={14} />
        {t("Write a new note")}
      </button>
      {writing && <NewNote done={closeForm} />}
      <Updating />
      <div class="overflow-auto flex-1 min-h-0">
        {!searching && (
          <>
            <FolderContent folder={buildTree(notes.notes)} depth={0} />
            {notes.notes.length === 0 && (
              <p class="text-sm text-[#9a9aaa] px-1">
                {t("No notes yet. Write the first one above.")}
              </p>
            )}
          </>
        )}
        {searching && (
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
