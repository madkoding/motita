// The right side of the memory window: the open entry read like a page of a wiki, with the way back,
// its collection, who wrote it and when, and the pages that link to it, or a welcome when none is open.
import { useEffect, useRef, useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { Markdown } from "./Markdown";
import { DotsMenu } from "./MemoryMenus";
import { IconBook } from "./icons";
import { GraphPane } from "./MemoryGraphPane";
import { SMALL_BTN } from "./MemoryLibrary";
import {
  parseEntryHref,
  placeOf,
  plainWiki,
  wikiToMarkdown,
  type EntryView,
} from "./memoryApi";

const FIELD =
  "w-full min-h-[40px] px-3 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#7a7a8c]";

// BACKLINKS_OPEN is how many pages that link here show open; more start folded so the page stays
// in view.
const BACKLINKS_OPEN = 5;

// Backlinks are the pages that link to the open one.
function Backlinks() {
  const { current, openEntry } = useApp().memory;
  const back = (current as EntryView).backlinks;
  return (
    <details
      class="entry-folder mt-4 pt-3 border-t border-white/5"
      open={back.length <= BACKLINKS_OPEN}
      data-testid="backlinks"
    >
      <summary class="flex items-center gap-2 text-sm text-[#9a9aaa] mb-1.5 cursor-pointer">
        {tf("Pages that link here ({n})", { n: String(back.length) })}
      </summary>
      {back.length === 0 && (
        <p class="text-sm text-[#7a7a8c]">
          {t("No other entry mentions this one yet.")}
        </p>
      )}
      <ul class="space-y-1">
        {back.map((b) => (
          <li key={b.path}>
            <button
              class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-white/5"
              data-testid="backlink"
              onClick={() => void openEntry(b.path)}
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

// EntryBody is the entry as text to edit, or drawn, with its [[links]] alive.
function EntryBody() {
  const { current, draft, setDraft, editing } = useApp().memory;
  if (editing) {
    return (
      <textarea
        class="flex-1 min-h-[40vh] w-full p-3 rounded-lg bg-black/30 border border-white/10 font-mono text-sm text-[#e8e8ea] resize-none"
        value={draft}
        aria-label={t("Entry text")}
        data-testid="entry-editor"
        spellcheck={false}
        onInput={(e) => setDraft((e.target as HTMLTextAreaElement).value)}
      />
    );
  }
  return <Preview body={wikiToMarkdown(draft, (current as EntryView).links)} />;
}

// Preview draws the entry and follows the [[links]] in it. The listeners are added to the page
// itself, since the page is not a control. Enter on a focused link follows it too, so keyboard
// users get there the same way.
function Preview({ body }: { body: string }) {
  const { followLink } = useApp().memory;
  const page = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = page.current as HTMLDivElement;
    const follow = (e: Event) => {
      const href = (e.target as Element).closest("a")?.getAttribute("href");
      if (parseEntryHref(href)) {
        e.preventDefault();
        void followLink(href);
      }
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Enter") {
        follow(e);
      }
    };
    el.addEventListener("click", follow);
    el.addEventListener("keydown", onKeyDown);
    return () => {
      el.removeEventListener("click", follow);
      el.removeEventListener("keydown", onKeyDown);
    };
  }, [followLink]);
  return (
    <div
      ref={page}
      class="skill-body artifact-body entry-preview relative p-3 md:px-5 rounded-lg bg-black/30"
      data-testid="entry-preview"
    >
      <Markdown content={body} />
    </div>
  );
}

// EntryToolbar is the top of a page: the way back on a phone, where the page is and what it is
// called, who wrote it and when, to edit it, and a "⋯" menu with the rest.
function EntryToolbar() {
  const {
    current,
    draft,
    editing,
    setEditing,
    back,
    saveEntry,
    renameEntry,
    askDelete,
  } = useApp().memory;
  const entry = current as EntryView;
  // The name being typed while renaming; null when the field is closed.
  const [name, setName] = useState<string | null>(null);
  const rename = (e: Event) => {
    e.preventDefault();
    void renameEntry(name as string);
    setName(null);
  };
  const place = placeOf(entry.path);
  const title = entry.title;
  const by = byline(entry);
  return (
    <div class="flex items-center gap-2 mb-2 flex-wrap">
      <button
        class={SMALL_BTN + " md:hidden"}
        data-testid="entry-back"
        onClick={() => void back()}
      >
        {t("Back to the list")}
      </button>
      <div
        class="min-w-0 basis-full order-first md:order-none md:basis-0 md:flex-1"
        data-testid="entry-place"
      >
        <div class="text-sm truncate">
          {place !== "" && <span class="text-[#9a9aaa]">{place + " › "}</span>}
          <span class="font-semibold text-[#e8e8ea]">{title}</span>
        </div>
        {by !== "" && (
          <div class="text-xs text-[#9a9aaa] truncate" data-testid="entry-by">
            {by}
          </div>
        )}
      </div>
      <button
        class={SMALL_BTN}
        aria-pressed={editing}
        data-testid="entry-mode"
        onClick={() => setEditing(!editing)}
      >
        {choose(editing, t("Preview"), t("Edit"))}
      </button>
      {current?.body !== draft && (
        <button
          class={SMALL_BTN + " bg-accent text-white border-accent"}
          data-testid="entry-save"
          onClick={() => void saveEntry()}
        >
          {t("Save")}
        </button>
      )}
      <DotsMenu
        label={t("Options for this entry")}
        testid="entry-menu"
        items={[
          {
            label: t("Rename"),
            hint: t("Links from other entries follow the new name"),
            testid: "entry-rename",
            onSelect: () => setName(entry.title),
          },
          {
            label: t("Delete"),
            hint: t("Asks before deleting it"),
            danger: true,
            testid: "entry-delete",
            onSelect: () =>
              askDelete({
                kind: "entry",
                path: entry.path,
                name: title,
                count: 1,
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
            data-testid="entry-rename-name"
            onInput={(e) => setName((e.target as HTMLInputElement).value)}
          />
          <button type="submit" class={SMALL_BTN} data-testid="entry-rename-go">
            {t("Rename")}
          </button>
        </form>
      )}
    </div>
  );
}

// byline says who wrote an entry and when, "" when the entry says neither.
function byline(entry: EntryView): string {
  const date = entry.meta?.date ? entry.meta.date.slice(0, 10) : "";
  return [entry.meta?.author, date].filter(Boolean).join(" · ");
}

// Welcome is what the window says when no entry is open: what the memory is, how it is organised,
// and where the files are.
function Welcome() {
  const { root } = useApp().memory;
  return (
    <div
      class="m-auto max-w-md text-center text-sm text-[#9a9aaa] space-y-3 px-2"
      data-testid="memory-welcome"
    >
      <IconBook size={36} class="mx-auto text-accent opacity-80" />
      <p class="text-base text-[#e8e8ea]">{t("Welcome to your memory")}</p>
      <p>
        {t(
          "Here Motita keeps what you and it want to remember. Search it by words or by meaning, and the answer is the whole section, with who wrote it and when.",
        )}
      </p>
      <p>
        {t(
          "Each top-level folder is a collection, so engineering and HR never mix. Link entries with [[double brackets]] and the map shows how they relate.",
        )}
      </p>
      {!!root && (
        <details class="text-xs text-left">
          <summary class="cursor-pointer text-center">
            {t("Where is my memory kept?")}
          </summary>
          <p class="mt-1.5 break-all">
            {tf(
              "It is ordinary text files, so other programs such as Obsidian can open them too. The folder is {root}",
              { root },
            )}
          </p>
        </details>
      )}
    </div>
  );
}

// OpenEntry is the right side: the open entry, the map when it is open, or the welcome.
export function OpenEntry() {
  const { current, graph } = useApp().memory;
  return (
    <section
      class={`${choose(!!current || !!graph, "flex", "hidden md:flex")} flex-col flex-1 min-w-0 min-h-0 md:pl-1`}
    >
      {!!graph && <GraphPane graph={graph} />}
      {!current && !graph && <Welcome />}
      {!!current && (
        <>
          <EntryToolbar />
          <div
            key={current.path}
            class="flex flex-col flex-1 min-h-0 overflow-auto"
          >
            <EntryBody />
            <Backlinks />
          </div>
        </>
      )}
    </section>
  );
}
