// The left side of the memory window: the search, with the collection, the author and the dates
// that narrow it, and the library of entries as folders, each with a "⋯" menu to delete it.
import { useCallback, useEffect, useState } from "preact/hooks";
import { plural, t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { DotsMenu, type MenuItem } from "./MemoryMenus";
import { IconFile, IconFolder, IconNotes } from "./icons";
import {
  buildTree,
  countEntries,
  plainWiki,
  type EntryFolder,
  type MemoryEntry,
  type MemoryHit,
} from "./memoryApi";

const FIELD =
  "w-full min-h-[40px] px-3 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#7a7a8c]";
export const SMALL_BTN =
  "px-3 min-h-[34px] rounded-lg border border-white/10 text-xs text-[#c8c8d2] hover:bg-white/5 active:scale-95 transition-transform";
const ROW =
  "w-full flex items-center gap-2 text-left px-2.5 min-h-[36px] rounded-lg text-sm ";

// collectionName is what a collection is called: the entries at the top of the memory are General.
export function collectionName(name: string): string {
  return choose(name === "", t("General"), name);
}

// EntryButton is one entry of the list; the open one is marked.
function EntryButton({ n }: { n: MemoryEntry }) {
  const { current, openEntry } = useApp().memory;
  const open = current?.path === n.path;
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
      data-testid="entry-row"
      onClick={() => void openEntry(n.path)}
    >
      <IconFile size={15} class="flex-none opacity-70" />
      <span class="truncate">{n.title}</span>
    </button>
  );
}

// FolderMenu is the "⋯" menu of a folder: delete it with what it holds.
function FolderMenu({ folder }: { folder: EntryFolder }) {
  const { askDelete } = useApp().memory;
  const count = countEntries(folder);
  const items: MenuItem[] = [
    {
      label: t("Delete this folder"),
      hint: plural(count, "Deletes its 1 entry", "Deletes its {n} entries"),
      danger: true,
      testid: "folder-delete",
      onSelect: () =>
        askDelete({
          kind: "folder",
          path: folder.path,
          name: folder.name,
          count,
        }),
    },
  ];
  return (
    <DotsMenu
      label={tf("Options for {name}", { name: folder.name })}
      testid="folder-menu"
      items={items}
    />
  );
}

// holds says whether the open entry is in the folder.
function holds(path: string | undefined, folder: EntryFolder): boolean {
  return !!path?.startsWith(folder.path + "/");
}

// useOpen keeps a folder open or closed as the person left it, and opens it when the open entry
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

// FolderNode is a folder of the list with its sub-folders and entries, collapsible. The collections
// at the top start open, deeper folders closed unless they hold the open entry.
function FolderNode({ folder, depth }: { folder: EntryFolder; depth: number }) {
  const { current } = useApp().memory;
  const state = useOpen(holds(current?.path, folder) || depth === 1);
  return (
    <details
      open={state.open}
      onToggle={state.onToggle}
      class="entry-folder"
      data-testid="entry-folder"
    >
      <summary class="px-2.5 min-h-[40px] flex items-center gap-2 text-sm text-[#c8c8d2] cursor-pointer rounded-lg hover:bg-white/5">
        <IconFolder size={15} class="flex-none text-accent" />
        <span class="truncate flex-1">{folder.name}</span>
        <span class="text-xs text-[#7a7a8c]">{countEntries(folder)}</span>
        <FolderMenu folder={folder} />
      </summary>
      <div class="pl-3 border-l border-white/5 ml-3.5">
        <FolderContent folder={folder} depth={depth} />
      </div>
    </details>
  );
}

// FolderContent lists a folder's sub-folders, then its entries.
function FolderContent({
  folder,
  depth,
}: {
  folder: EntryFolder;
  depth: number;
}) {
  return (
    <>
      {folder.folders.map((f) => (
        <FolderNode key={f.path} folder={f} depth={depth + 1} />
      ))}
      {[...folder.entries]
        .sort((a, b) => a.title.localeCompare(b.title))
        .map((n) => (
          <EntryButton key={n.path} n={n} />
        ))}
    </>
  );
}

// byline says who wrote a section and when.
function byline(h: MemoryHit): string {
  const date = (h.date ?? "").slice(0, 10);
  return [h.author, date].filter(Boolean).join(" · ");
}

// HitRow is one section the search found: where it is, who wrote it and when, and what matched.
function HitRow({ h }: { h: MemoryHit }) {
  const { openEntry } = useApp().memory;
  const place = [h.namespace, h.heading].filter(Boolean).join(" › ");
  const by = byline(h);
  return (
    <li>
      <button
        class="w-full text-left px-2.5 py-1.5 rounded-lg hover:bg-white/5"
        data-testid="memory-hit"
        onClick={() => void openEntry(h.path)}
      >
        <div class="text-sm text-[#e8e8ea] truncate">{h.title}</div>
        {place !== "" && (
          <div class="text-xs text-accent/80 truncate">{place}</div>
        )}
        {!!h.snippet && (
          <div class="text-xs text-[#9a9aaa] line-clamp-2">
            {plainWiki(h.snippet)}
          </div>
        )}
        {(by !== "" || !!h.via) && (
          <div class="text-[11px] text-[#7a7a8c] truncate">
            {[by, !!h.via && tf("via {name}", { name: h.via as string })]
              .filter(Boolean)
              .join(" · ")}
          </div>
        )}
      </button>
    </li>
  );
}

// NewEntry is the form that makes an entry, in a collection that exists or a new one.
function NewEntry({ done }: { done: () => void }) {
  const { namespaces, createEntry } = useApp().memory;
  const [title, setTitle] = useState("");
  const [folder, setFolder] = useState("");
  const submit = (e: Event) => {
    e.preventDefault();
    void createEntry(folder, title);
    setTitle("");
    done();
  };
  return (
    <form class="flex flex-col gap-2" onSubmit={submit}>
      <input
        class={FIELD}
        value={title}
        placeholder={t("What is the entry about?")}
        aria-label={t("New entry name")}
        data-testid="entry-new-name"
        onInput={(e) => setTitle((e.target as HTMLInputElement).value)}
      />
      <div class="flex gap-2">
        <input
          class={FIELD + " flex-1 min-w-0"}
          value={folder}
          list="memory-collections"
          placeholder={t("Collection (optional)")}
          aria-label={t("Collection")}
          data-testid="entry-new-folder"
          onInput={(e) => setFolder((e.target as HTMLInputElement).value)}
        />
        <datalist id="memory-collections">
          {namespaces
            .filter((n) => n.name !== "")
            .map((n) => (
              <option key={n.name} value={n.name} />
            ))}
        </datalist>
        <button
          type="submit"
          class="flex-none px-4 min-h-[40px] rounded-lg bg-accent text-white text-sm active:scale-95 transition-transform"
          data-testid="entry-create"
        >
          {t("Add")}
        </button>
      </div>
    </form>
  );
}

// LibraryMenu is what can be done with all the memory at once.
function LibraryMenu() {
  const app = useApp();
  return (
    <DotsMenu
      label={t("More options for the memory")}
      testid="library-menu"
      items={[
        {
          label: t("Review the memory"),
          hint: t(
            "Ask the agent to find contradictions, stale entries and broken links",
          ),
          testid: "memory-review",
          onSelect: () => void app.reviewMemory(),
        },
      ]}
    />
  );
}

// Filters narrow a search: the collection, whether to follow the links to related entries, and,
// folded away, the author and the dates.
function Filters() {
  const { namespaces, filters, filter } = useApp().memory;
  const text = (key: "author" | "since" | "until") => (e: Event) =>
    void filter({ [key]: (e.target as HTMLInputElement).value });
  return (
    <div class="flex flex-col gap-2" data-testid="memory-filters">
      <div class="flex items-center gap-2">
        <select
          class={FIELD + " flex-1 min-w-0"}
          value={filters.namespace}
          aria-label={t("Collection")}
          data-testid="filter-namespace"
          onChange={(e) =>
            void filter({ namespace: (e.target as HTMLSelectElement).value })
          }
        >
          <option value="">{t("All collections")}</option>
          {namespaces.map((n) => (
            <option key={n.name} value={n.name}>
              {tf("{name} ({n})", {
                name: collectionName(n.name),
                n: String(n.entries),
              })}
            </option>
          ))}
        </select>
        <label
          class="flex-none flex items-center gap-1.5 text-xs text-[#c8c8d2]"
          title={t("Also show the entries linked to the best answers")}
        >
          <input
            type="checkbox"
            checked={filters.graph}
            data-testid="filter-graph"
            onChange={(e) =>
              void filter({ graph: (e.target as HTMLInputElement).checked })
            }
          />
          {t("Follow links")}
        </label>
      </div>
      <details class="text-xs text-[#9a9aaa]">
        <summary class="cursor-pointer">{t("More filters")}</summary>
        <div class="mt-2 flex flex-col gap-2">
          <input
            class={FIELD}
            value={filters.author}
            placeholder={t("Author")}
            aria-label={t("Author")}
            data-testid="filter-author"
            onInput={text("author")}
          />
          <div class="flex gap-2">
            <input
              type="date"
              class={FIELD}
              value={filters.since}
              aria-label={t("Written since")}
              data-testid="filter-since"
              onInput={text("since")}
            />
            <input
              type="date"
              class={FIELD}
              value={filters.until}
              aria-label={t("Written until")}
              data-testid="filter-until"
              onInput={text("until")}
            />
          </div>
        </div>
      </details>
    </div>
  );
}

// Results is the answer to a search: the sections found, then the ones reached through links.
function Results() {
  const { answer } = useApp().memory;
  const related = answer.related ?? [];
  return (
    <ul>
      {answer.hits.map((h) => (
        <HitRow key={h.path + ":" + h.line} h={h} />
      ))}
      {answer.hits.length === 0 && (
        <p class="text-sm text-[#9a9aaa] px-1">{t("Nothing matches that.")}</p>
      )}
      {related.length > 0 && (
        <>
          <li
            class="px-2.5 pt-3 pb-1 text-xs text-[#9a9aaa]"
            data-testid="memory-related"
          >
            {t("Related through links")}
          </li>
          {related.map((h) => (
            <HitRow key={"rel:" + h.path} h={h} />
          ))}
        </>
      )}
    </ul>
  );
}

// MemoryList is the left side: the search and its filters, the new entry form and the library or
// the search results.
export function MemoryList() {
  const { memory } = useApp();
  const { current, graph, query, entries, search } = memory;
  const [writing, setWriting] = useState(false);
  const closeForm = useCallback(() => setWriting(false), []);
  function toggleForm() {
    setWriting(!writing);
  }
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
          placeholder={t("Search the memory")}
          aria-label={t("Search the memory")}
          data-testid="memory-search"
          onInput={(e) => void search((e.target as HTMLInputElement).value)}
        />
        <LibraryMenu />
      </div>
      <Filters />
      <button
        class={SMALL_BTN + " flex items-center justify-center gap-2"}
        aria-expanded={writing}
        data-testid="entry-new-toggle"
        onClick={toggleForm}
      >
        <IconNotes size={14} />
        {t("Write a new entry")}
      </button>
      {writing && <NewEntry done={closeForm} />}
      <div class="overflow-auto flex-1 min-h-0">
        {!searching && (
          <>
            <FolderContent folder={buildTree(entries)} depth={0} />
            {entries.length === 0 && (
              <p class="text-sm text-[#9a9aaa] px-1">
                {t("Nothing in the memory yet. Write the first entry above.")}
              </p>
            )}
          </>
        )}
        {searching && <Results />}
      </div>
    </aside>
  );
}
