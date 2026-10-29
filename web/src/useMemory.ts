// The memory: the list of entries, the one open with its draft, the search and the actions on
// them. Entries are plain Markdown files on the gateway's disk; this hook only asks for them.
import { useCallback, useRef, useState } from "preact/hooks";
import { plural, t } from "./i18n";
import { HTTP_CONFLICT, JSON_HEADERS } from "./appConst";
import type { SetStatus } from "./appTypes";
import type { Deletion } from "./MemoryMenus";
import {
  entryFileName,
  folderOf,
  NO_FILTERS,
  parseEntryHref,
  searchPath,
  type Api,
  type EntryView,
  type MemoryAnswer,
  type MemoryEntry,
  type MemoryFilters,
  type MemoryGraph,
  type Namespace,
} from "./memoryApi";

const MEMORY_PATH = "/v1/memory";
const ENTRY_EXT = ".md";

const entryUrl = (path: string) =>
  MEMORY_PATH + "/entry?path=" + encodeURIComponent(path);

const NO_ANSWER: MemoryAnswer = { hits: [] };

// useMemorySearch is the search box and its filters: the answer on screen is the newest one asked.
function useMemorySearch(api: Api) {
  const [query, setQuery] = useState("");
  const [filters, setFilters] = useState<MemoryFilters>(NO_FILTERS);
  const [answer, setAnswer] = useState<MemoryAnswer>(NO_ANSWER);
  // The newest search is the only one that may paint: answers can arrive out of order.
  const searchSeq = useRef(0);
  // The query and filters of the search on screen, so changing one reruns it with the other.
  const asked = useRef({ query, filters });
  asked.current = { query, filters };

  // runSearch asks the memory what it says about q under the filters.
  const runSearch = useCallback(
    async (q: string, f: MemoryFilters) => {
      searchSeq.current += 1;
      const seq = searchSeq.current;
      if (q.trim() === "") {
        setAnswer(NO_ANSWER);
        return;
      }
      try {
        const res = await api(searchPath(q, f));
        let d = NO_ANSWER;
        if (res.ok) {
          d = await res.json();
        }
        if (seq === searchSeq.current) {
          setAnswer(d);
        }
      } catch {
        if (seq === searchSeq.current) {
          setAnswer(NO_ANSWER);
        }
      }
    },
    [api],
  );

  const search = useCallback(
    async (q: string) => {
      setQuery(q);
      await runSearch(q, asked.current.filters);
    },
    [runSearch],
  );

  // filter changes some of the filters and runs the search again.
  const filter = useCallback(
    async (change: Partial<MemoryFilters>) => {
      const next = { ...asked.current.filters, ...change };
      setFilters(next);
      await runSearch(asked.current.query, next);
    },
    [runSearch],
  );

  // reset forgets the query, the filters and the answer.
  const reset = useCallback(() => {
    setQuery("");
    setFilters(NO_FILTERS);
    setAnswer(NO_ANSWER);
  }, []);

  return { query, filters, answer, search, filter, reset };
}

// useEntryEdits makes, renames and deletes entries. save, loadList and fetchEntry are the main
// hook's, and setCurrent closes the entry on screen when it is the one that went.
function useEntryEdits(
  api: Api,
  setState: SetStatus,
  live: { current: { current: EntryView | null; draft: string } },
  steps: {
    save: () => Promise<boolean>;
    loadList: () => Promise<void>;
    fetchEntry: (path: string, edit: boolean) => Promise<void>;
    setCurrent: (v: EntryView | null) => void;
    setPending: (v: Deletion | null) => void;
  },
) {
  const { save, loadList, fetchEntry, setCurrent, setPending } = steps;

  // createEntry makes an entry called title in folder, and opens it ready to be written.
  const createEntry = useCallback(
    async (folder: string, title: string) => {
      const path = entryFileName(folder, title);
      if (path === "") {
        setState(t("give the entry a name"), true);
        return;
      }
      if (!(await save())) {
        return;
      }
      try {
        const name = path.slice(path.lastIndexOf("/") + 1, -ENTRY_EXT.length);
        const res = await api(entryUrl(path), {
          method: "PUT",
          headers: JSON_HEADERS,
          body: JSON.stringify({ body: "# " + name + "\n\n", create: true }),
        });
        if (res.status === HTTP_CONFLICT) {
          setState(t("an entry with that name already exists"), true);
          return;
        }
        if (!res.ok) {
          setState(t("could not create the entry"), true);
          return;
        }
        await loadList();
        await fetchEntry(path, true);
      } catch {
        setState(t("could not create the entry"), true);
      }
    },
    [api, save, loadList, fetchEntry, setState],
  );

  // renameEntry gives the open entry another name in the same folder. The gateway rewrites the
  // [[links]] that led to it, so the entry is shown again to get its new links and backlinks.
  const renameEntry = useCallback(
    async (title: string) => {
      const entry = live.current.current;
      if (!entry) {
        return;
      }
      const to = entryFileName(folderOf(entry.path), title);
      if (to === "") {
        setState(t("give the entry a name"), true);
        return;
      }
      if (to === entry.path || !(await save())) {
        return;
      }
      try {
        const res = await api(MEMORY_PATH + "/rename", {
          method: "POST",
          headers: JSON_HEADERS,
          body: JSON.stringify({ from: entry.path, to }),
        });
        if (res.status === HTTP_CONFLICT) {
          setState(t("an entry with that name already exists"), true);
          return;
        }
        if (!res.ok) {
          setState(t("could not rename the entry"), true);
          return;
        }
        await loadList();
        await fetchEntry(to, false);
      } catch {
        setState(t("could not rename the entry"), true);
      }
    },
    [api, save, loadList, fetchEntry, setState],
  );

  const deleteEntry = useCallback(async () => {
    const entry = live.current.current;
    if (!entry) {
      return;
    }
    try {
      const res = await api(entryUrl(entry.path), { method: "DELETE" });
      if (!res.ok) {
        setState(t("could not delete the entry"), true);
        return;
      }
      setCurrent(null);
      await loadList();
    } catch {
      setState(t("could not delete the entry"), true);
    }
  }, [api, loadList, setState]);

  // deleteFolder removes every entry of a folder. When the open entry was one of them it closes.
  const deleteFolder = useCallback(
    async (path: string, count: number) => {
      try {
        const res = await api(
          MEMORY_PATH + "/folder?path=" + encodeURIComponent(path),
          { method: "DELETE" },
        );
        if (!res.ok) {
          setState(t("could not delete the folder"), true);
          return;
        }
        if (live.current.current?.path.startsWith(path + "/")) {
          setCurrent(null);
        }
        setState(plural(count, "1 entry deleted", "{n} entries deleted"));
        await loadList();
      } catch {
        setState(t("could not delete the folder"), true);
      }
    },
    [api, loadList, setState],
  );

  // confirmDelete deletes what the person was asked about: the open entry, or a folder of entries.
  const confirmDelete = useCallback(
    async (d: Deletion) => {
      setPending(null);
      if (d.kind === "entry") {
        await deleteEntry();
      } else {
        await deleteFolder(d.path, d.count);
      }
    },
    [deleteEntry, deleteFolder],
  );

  return { createEntry, renameEntry, deleteEntry, deleteFolder, confirmDelete };
}

export function useMemory(api: Api, setState: SetStatus) {
  const [showMemory, setShowMemory] = useState(false);
  const [entries, setEntries] = useState<MemoryEntry[]>([]);
  const [namespaces, setNamespaces] = useState<Namespace[]>([]);
  const [root, setRoot] = useState("");
  // Whether the last look at the list failed, so an unreadable memory is not shown as an empty one.
  const [loadFailed, setLoadFailed] = useState(false);
  // What the person asked to delete and has not yet confirmed.
  const [pending, setPending] = useState<Deletion | null>(null);
  const [current, setCurrent] = useState<EntryView | null>(null);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  // The graph of links, when it is the view on screen.
  const [graph, setGraph] = useState<MemoryGraph | null>(null);
  const { query, filters, answer, search, filter, reset } =
    useMemorySearch(api);
  // The draft and the entry it belongs to, for the save that happens when the person moves on.
  const live = useRef({ current, draft });
  live.current = { current, draft };

  const loadList = useCallback(async () => {
    try {
      const res = await api(MEMORY_PATH);
      if (!res.ok) {
        setLoadFailed(true);
        setState(t("could not load the memory"), true);
        return;
      }
      const d = await res.json();
      setEntries(d.entries);
      setNamespaces(d.namespaces ?? []);
      setRoot(d.root);
      setLoadFailed(false);
    } catch {
      setLoadFailed(true);
      setState(t("could not load the memory"), true);
    }
  }, [api, setState]);

  // fetchEntry reads an entry and puts it on screen.
  const fetchEntry = useCallback(
    async (path: string, edit: boolean) => {
      try {
        const res = await api(entryUrl(path));
        if (!res.ok) {
          setState(t("could not open the entry"), true);
          return;
        }
        const view: EntryView = await res.json();
        setGraph(null);
        setCurrent(view);
        setDraft(view.body);
        setEditing(edit);
      } catch {
        setState(t("could not open the entry"), true);
      }
    },
    [api, setState],
  );

  // save writes the draft of the open entry, and reports whether it was kept.
  const save = useCallback(async (): Promise<boolean> => {
    const { current: entry, draft: body } = live.current;
    if (!entry || body === entry.body) {
      return true;
    }
    try {
      const res = await api(entryUrl(entry.path), {
        method: "PUT",
        headers: JSON_HEADERS,
        body: JSON.stringify({ body }),
      });
      if (!res.ok) {
        setState(t("could not save the entry"), true);
        return false;
      }
      return true;
    } catch {
      setState(t("could not save the entry"), true);
      return false;
    }
  }, [api, setState]);

  // saveEntry saves and shows the entry again, so its links and backlinks are the new ones.
  const saveEntry = useCallback(async () => {
    const entry = live.current.current;
    if (entry && (await save())) {
      await fetchEntry(entry.path, true);
    }
  }, [save, fetchEntry]);

  // openEntry moves to another entry, keeping what was typed in the one being left.
  const openEntry = useCallback(
    async (path: string, edit = false) => {
      if (await save()) {
        await fetchEntry(path, edit);
      }
    },
    [save, fetchEntry],
  );

  // peekEntry reads an entry without opening it, for a look at it beside the map; null when it cannot.
  const peekEntry = useCallback(
    async (path: string): Promise<EntryView | null> => {
      try {
        const res = await api(entryUrl(path));
        if (!res.ok) {
          return null;
        }
        return (await res.json()) as EntryView;
      } catch {
        return null;
      }
    },
    [api],
  );

  const openMemory = useCallback(() => {
    setShowMemory(true);
    void loadList();
  }, [loadList]);

  // closeMemory says whether the window closed: it stays when the open entry could not be saved.
  const closeMemory = useCallback(async () => {
    if (!(await save())) {
      return false;
    }
    setShowMemory(false);
    setCurrent(null);
    setGraph(null);
    reset();
    return true;
  }, [save]);

  // openGraph shows the entries as a graph of their links.
  const openGraph = useCallback(async () => {
    if (!(await save())) {
      return;
    }
    try {
      const res = await api(MEMORY_PATH + "/graph");
      if (!res.ok) {
        setState(t("could not load the graph"), true);
        return;
      }
      setCurrent(null);
      setGraph(await res.json());
    } catch {
      setState(t("could not load the graph"), true);
    }
  }, [api, save, setState]);

  const closeGraph = useCallback(() => setGraph(null), []);

  // back leaves the open entry for the list (the way out of an entry on a phone).
  const back = useCallback(async () => {
    if (await save()) {
      setCurrent(null);
      void loadList();
    }
  }, [save, loadList]);

  const { createEntry, renameEntry, deleteEntry, deleteFolder, confirmDelete } =
    useEntryEdits(api, setState, live, {
      save,
      loadList,
      fetchEntry,
      setCurrent,
      setPending,
    });

  // followLink opens the entry a [[link]] of the preview leads to, or makes it when it is new.
  const followLink = useCallback(
    async (href: string | null | undefined) => {
      const to = parseEntryHref(href);
      if (!to) {
        return;
      }
      if (to.kind === "entry") {
        await openEntry(to.path);
      } else {
        await createEntry(
          folderOf(live.current.current?.path ?? ""),
          to.target,
        );
      }
    },
    [openEntry, createEntry],
  );

  return {
    showMemory,
    entries,
    namespaces,
    root,
    loadFailed,
    reload: loadList,
    current,
    draft,
    setDraft,
    editing,
    setEditing,
    query,
    filters,
    answer,
    graph,
    openGraph,
    closeGraph,
    openMemory,
    closeMemory,
    back,
    openEntry,
    peekEntry,
    saveEntry,
    createEntry,
    renameEntry,
    deleteEntry,
    deleteFolder,
    pending,
    askDelete: setPending,
    confirmDelete,
    search,
    filter,
    followLink,
  };
}
