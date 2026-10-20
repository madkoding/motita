// The right side of the notes window: the open note read like a page of a wiki, with the way back,
// its place in the library and the pages that link to it, or a welcome when none is open.
import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { Markdown } from "./Markdown";
import { DotsMenu } from "./NoteMenus";
import { IconBook } from "./icons";
import { GraphPane } from "./NoteGraphPane";
import { placeOf, readTitle, SMALL_BTN } from "./NotesLibrary";
import {
  parseNoteHref,
  plainWiki,
  START_NOTE,
  wikiToMarkdown,
  type NoteView,
} from "./notesApi";

const FIELD =
  "w-full min-h-[40px] px-3 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#7a7a8c]";

// BACKLINKS_OPEN is how many pages that link here show open; more start folded so the page stays
// in view.
const BACKLINKS_OPEN = 5;

// Backlinks are the pages that link to the open one.
function Backlinks() {
  const { current, openNote, folders } = useApp().notes;
  const back = (current as NoteView).backlinks;
  return (
    <details
      class="note-folder mt-4 pt-3 border-t border-white/5"
      open={back.length <= BACKLINKS_OPEN}
      data-testid="backlinks"
    >
      <summary class="flex items-center gap-2 text-sm text-[#9a9aaa] mb-1.5 cursor-pointer">
        {tf("Pages that link here ({n})", { n: String(back.length) })}
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
              <div class="text-sm text-accent truncate">
                {readTitle(b, folders)}
              </div>
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
  const follow = (e: Event) => {
    const href = (e.target as Element).closest("a")?.getAttribute("href");
    if (parseNoteHref(href)) {
      e.preventDefault();
      void followLink(href);
    }
  };
  // Enter on a focused link is handled here too, so keyboard users follow it the same way.
  const onKeyDown = (e: KeyboardEvent) => {
    if (e.key === "Enter") {
      follow(e);
    }
  };
  return (
    <div
      class="skill-body artifact-body note-preview relative p-3 md:px-5 rounded-lg bg-black/30"
      data-testid="note-preview"
      role="presentation"
      onClick={follow}
      onKeyDown={onKeyDown}
    >
      <Markdown content={wikiToMarkdown(draft, (current as NoteView).links)} />
    </div>
  );
}

// NoteToolbar is the top of a page: the way back on a phone, where the page is and what it is
// called, to edit it, and a "⋯" menu with the rest.
function NoteToolbar() {
  const {
    current,
    draft,
    editing,
    setEditing,
    back,
    saveNote,
    renameNote,
    askDelete,
    folders,
  } = useApp().notes;
  const note = current as NoteView;
  // The name being typed while renaming; null when the field is closed.
  const [name, setName] = useState<string | null>(null);
  const rename = (e: Event) => {
    e.preventDefault();
    void renameNote(name as string);
    setName(null);
  };
  const place = placeOf(note.path, folders);
  const title = readTitle(note, folders);
  return (
    <div class="flex items-center gap-2 mb-2 flex-wrap">
      <button
        class={SMALL_BTN + " md:hidden"}
        data-testid="note-back"
        onClick={() => void back()}
      >
        {t("Back to the list")}
      </button>
      <div
        class="min-w-0 basis-full order-first md:order-none md:basis-0 md:flex-1"
        data-testid="note-place"
      >
        <div class="text-sm truncate">
          {place !== "" && <span class="text-[#9a9aaa]">{place + " › "}</span>}
          <span class="font-semibold text-[#e8e8ea]">{title}</span>
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
      {current?.body !== draft && (
        <button
          class={SMALL_BTN + " bg-accent text-white border-accent"}
          data-testid="note-save"
          onClick={() => void saveNote()}
        >
          {t("Save")}
        </button>
      )}
      <DotsMenu
        label={t("Options for this note")}
        testid="note-menu"
        items={[
          {
            label: t("Rename"),
            hint: t("Links from other notes follow the new name"),
            testid: "note-rename",
            onSelect: () => setName(note.title),
          },
          {
            label: t("Delete"),
            hint: t("Asks before deleting it"),
            danger: true,
            testid: "note-delete",
            onSelect: () =>
              askDelete({
                kind: "note",
                path: note.path,
                name: title,
                count: 1,
                rewritable: false,
              }),
          },
        ]}
      />
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

// Welcome is what the window says when no note is open: what the notes are, a guide to start with
// or how to get one, and where the files are.
function Welcome() {
  const { folders, notes, openNote, rescanFolder, root } = useApp().notes;
  const guides = folders.filter(
    (f) =>
      f.kind === "guide" && notes.some((n) => n.path.startsWith(f.path + "/")),
  );
  return (
    <div
      class="m-auto max-w-md text-center text-sm text-[#9a9aaa] space-y-3 px-2"
      data-testid="notes-welcome"
    >
      <IconBook size={36} class="mx-auto text-accent opacity-80" />
      <p class="text-base text-[#e8e8ea]">{t("Welcome to your notes")}</p>
      <p>
        {t(
          "Here Motita keeps what it learns about your projects, and you can write your own notes.",
        )}
      </p>
      {guides.length > 0 && (
        <>
          <p>
            {t(
              "Start with a guide: it explains a project in plain words, one chapter for each part.",
            )}
          </p>
          <div class="flex flex-col gap-2">
            {guides.map((g) => (
              <button
                key={g.path}
                class={SMALL_BTN + " text-sm"}
                data-testid="welcome-guide"
                onClick={() => void openNote(g.path + "/" + START_NOTE + ".md")}
              >
                {tf("Open the guide of {name}", { name: g.title ?? g.path })}
              </button>
            ))}
          </div>
        </>
      )}
      {guides.length === 0 && (
        <>
          <p>
            {t(
              "There are no guides yet. Motita can write one for each of your projects.",
            )}
          </p>
          <button
            class={SMALL_BTN + " text-sm"}
            data-testid="welcome-write"
            onClick={() => void rescanFolder("Projects")}
          >
            {t("Write the guides now")}
          </button>
        </>
      )}
      {!!root && (
        <details class="text-xs text-left">
          <summary class="cursor-pointer text-center">
            {t("Where are my notes kept?")}
          </summary>
          <p class="mt-1.5 break-all">
            {tf(
              "They are ordinary text files, so other programs such as Obsidian can open them too. The folder is {root}",
              { root },
            )}
          </p>
        </details>
      )}
    </div>
  );
}

// OpenNote is the right side: the open note, the map when it is open, or the welcome.
export function OpenNote() {
  const { current, graph } = useApp().notes;
  return (
    <section
      class={`${choose(!!current || !!graph, "flex", "hidden md:flex")} flex-col flex-1 min-w-0 min-h-0 md:pl-1`}
    >
      {!!graph && <GraphPane graph={graph} />}
      {!current && !graph && <Welcome />}
      {!!current && (
        <>
          <NoteToolbar />
          <div
            key={current.path}
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
