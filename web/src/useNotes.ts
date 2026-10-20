// The note vault: the list of notes, the one open with its draft, the search and the actions on
// them. Notes are plain Markdown files on the gateway's disk; this hook only asks for them.
import { useCallback, useRef, useState } from "preact/hooks";
import { plural, t } from "./i18n";
import { HTTP_CONFLICT, JSON_HEADERS } from "./appConst";
import type { SetStatus } from "./appTypes";
import type { Deletion } from "./NoteMenus";
import { scanPoll } from "./NotesScanSettings";
import {
  folderOf,
  noteFileName,
  parseNoteHref,
  SCAN_STARTED_EVENT,
  scanNotes,
  scanProgress,
  type Api,
  type NoteGraph,
  type NoteHit,
  type NoteItem,
  type NoteView,
  type ScanFolder,
  type ScanProgress,
} from "./notesApi";

const NOTES_PATH = "/v1/notes";

const noteUrl = (path: string) =>
  NOTES_PATH + "/note?path=" + encodeURIComponent(path);

export function useNotes(api: Api, setState: SetStatus) {
  const [showNotes, setShowNotes] = useState(false);
  const [notes, setNotes] = useState<NoteItem[]>([]);
  const [root, setRoot] = useState("");
  // The folders a scan writes, and how far the scan that is writing them got.
  const [folders, setFolders] = useState<ScanFolder[]>([]);
  const [updating, setUpdating] = useState<ScanProgress | null>(null);
  const watching = useRef(false);
  // What the person asked to delete and has not yet confirmed.
  const [pending, setPending] = useState<Deletion | null>(null);
  const [current, setCurrent] = useState<NoteView | null>(null);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<NoteHit[]>([]);
  // The graph of links, when it is the view on screen.
  const [graph, setGraph] = useState<NoteGraph | null>(null);
  // The newest search is the only one that may paint: answers can arrive out of order.
  const searchSeq = useRef(0);
  // The draft and the note it belongs to, for the save that happens when the person moves on.
  const live = useRef({ current, draft });
  live.current = { current, draft };

  // loadList reads the notes and answers how many there are, or -1 when it could not tell.
  const loadList = useCallback(async (): Promise<number> => {
    try {
      const res = await api(NOTES_PATH);
      if (res.ok) {
        const d = await res.json();
        setNotes(d.notes);
        setRoot(d.root);
        setFolders(d.folders ?? []);
        return d.notes.length;
      }
    } catch {
      setState(t("could not load the notes"), true);
    }
    return -1;
  }, [api]);

  // fetchNote reads a note and puts it on screen.
  const fetchNote = useCallback(
    async (path: string, edit: boolean) => {
      try {
        const res = await api(noteUrl(path));
        if (!res.ok) {
          setState(t("could not open the note"), true);
          return;
        }
        const view: NoteView = await res.json();
        setGraph(null);
        setCurrent(view);
        setDraft(view.body);
        setEditing(edit);
      } catch {
        setState(t("could not open the note"), true);
      }
    },
    [api, setState],
  );

  // save writes the draft of the open note, and reports whether it was kept.
  const save = useCallback(async (): Promise<boolean> => {
    const { current: note, draft: body } = live.current;
    if (!note || body === note.body) {
      return true;
    }
    try {
      const res = await api(noteUrl(note.path), {
        method: "PUT",
        headers: JSON_HEADERS,
        body: JSON.stringify({ body }),
      });
      if (!res.ok) {
        setState(t("could not save the note"), true);
        return false;
      }
      return true;
    } catch {
      setState(t("could not save the note"), true);
      return false;
    }
  }, [api, setState]);

  // saveNote saves and shows the note again, so its links and backlinks are the new ones.
  const saveNote = useCallback(async () => {
    const note = live.current.current;
    if (note && (await save())) {
      await fetchNote(note.path, true);
    }
  }, [save, fetchNote]);

  // openNote moves to another note, keeping what was typed in the one being left.
  const openNote = useCallback(
    async (path: string, edit = false) => {
      if (await save()) {
        await fetchNote(path, edit);
      }
    },
    [save, fetchNote],
  );

  // peekNote reads a note without opening it, for a look at it beside the map; null when it cannot.
  const peekNote = useCallback(
    async (path: string): Promise<NoteView | null> => {
      try {
        const res = await api(noteUrl(path));
        if (!res.ok) {
          return null;
        }
        return (await res.json()) as NoteView;
      } catch {
        return null;
      }
    },
    [api],
  );

  // fillIfNew fills an empty vault from the sessions and projects the first time it is opened. It
  // is a courtesy: when it fails the person still has their empty vault and the Settings button.
  const fillIfNew = useCallback(
    async (count: number) => {
      if (count !== 0) {
        return;
      }
      try {
        if ((await scanNotes(api, { first: true })).ran) {
          setState(t("notes created from your sessions and projects"));
          await loadList();
        }
      } catch {
        // the Settings button reports a failing scan
      }
    },
    [api, loadList, setState],
  );

  const openNotes = useCallback(() => {
    setShowNotes(true);
    void loadList().then(fillIfNew);
  }, [loadList, fillIfNew]);

  // closeNotes says whether the window closed: it stays when the open note could not be saved.
  const closeNotes = useCallback(async () => {
    if (!(await save())) {
      return false;
    }
    setShowNotes(false);
    setCurrent(null);
    setGraph(null);
    setQuery("");
    setHits([]);
    return true;
  }, [save]);

  // openGraph shows the notes as a graph of their links.
  const openGraph = useCallback(async () => {
    if (!(await save())) {
      return;
    }
    try {
      const res = await api(NOTES_PATH + "/graph");
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

  // back leaves the open note for the list (the way out of a note on a phone).
  const back = useCallback(async () => {
    if (await save()) {
      setCurrent(null);
      void loadList();
    }
  }, [save, loadList]);

  // createNote makes a note called title in folder, and opens it ready to be written.
  const createNote = useCallback(
    async (folder: string, title: string) => {
      const path = noteFileName(folder, title);
      if (path === "") {
        setState(t("give the note a name"), true);
        return;
      }
      if (!(await save())) {
        return;
      }
      try {
        const name = path.slice(path.lastIndexOf("/") + 1, -3);
        const res = await api(noteUrl(path), {
          method: "PUT",
          headers: JSON_HEADERS,
          body: JSON.stringify({ body: "# " + name + "\n\n", create: true }),
        });
        if (res.status === HTTP_CONFLICT) {
          setState(t("a note with that name already exists"), true);
          return;
        }
        if (!res.ok) {
          setState(t("could not create the note"), true);
          return;
        }
        await loadList();
        await fetchNote(path, true);
      } catch {
        setState(t("could not create the note"), true);
      }
    },
    [api, save, loadList, fetchNote, setState],
  );

  // renameNote gives the open note another name in the same folder. The gateway rewrites the
  // [[links]] that led to it, so the note is shown again to get its new links and backlinks.
  const renameNote = useCallback(
    async (title: string) => {
      const note = live.current.current;
      if (!note) {
        return;
      }
      const to = noteFileName(folderOf(note.path), title);
      if (to === "") {
        setState(t("give the note a name"), true);
        return;
      }
      if (to === note.path || !(await save())) {
        return;
      }
      try {
        const res = await api(NOTES_PATH + "/rename", {
          method: "POST",
          headers: JSON_HEADERS,
          body: JSON.stringify({ from: note.path, to }),
        });
        if (res.status === HTTP_CONFLICT) {
          setState(t("a note with that name already exists"), true);
          return;
        }
        if (!res.ok) {
          setState(t("could not rename the note"), true);
          return;
        }
        await loadList();
        await fetchNote(to, false);
      } catch {
        setState(t("could not rename the note"), true);
      }
    },
    [api, save, loadList, fetchNote, setState],
  );

  const deleteNote = useCallback(async () => {
    const note = live.current.current;
    if (!note) {
      return;
    }
    try {
      const res = await api(noteUrl(note.path), { method: "DELETE" });
      if (!res.ok) {
        setState(t("could not delete the note"), true);
        return;
      }
      setCurrent(null);
      await loadList();
    } catch {
      setState(t("could not delete the note"), true);
    }
  }, [api, loadList, setState]);

  // deleteFolder removes every note of a folder. When the open note was one of them it closes.
  const deleteFolder = useCallback(
    async (path: string, count: number) => {
      try {
        const res = await api(
          NOTES_PATH + "/folder?path=" + encodeURIComponent(path),
          {
            method: "DELETE",
          },
        );
        if (!res.ok) {
          setState(t("could not delete the folder"), true);
          return;
        }
        if (live.current.current?.path.startsWith(path + "/")) {
          setCurrent(null);
        }
        setState(plural(count, "1 note deleted", "{n} notes deleted"));
        await loadList();
      } catch {
        setState(t("could not delete the folder"), true);
      }
    },
    [api, loadList, setState],
  );

  // watchScan follows the scan running in the background until it ends, then reads the list again
  // so the notes it wrote are there.
  const watchScan = useCallback(async () => {
    if (watching.current) {
      return;
    }
    watching.current = true;
    for (;;) {
      const p = await scanProgress(api).catch(() => null);
      if (!p?.running) {
        break;
      }
      setUpdating(p);
      await new Promise((r) => setTimeout(r, scanPoll.ms));
    }
    watching.current = false;
    setUpdating(null);
    await loadList();
  }, [api, loadList]);

  // rescanFolder writes a folder a scan wrote again, in the background: a guide for its project,
  // the others for all of them.
  const rescanFolder = useCallback(
    async (path: string) => {
      try {
        const res = await api(
          NOTES_PATH + "/folder/rescan?path=" + encodeURIComponent(path),
          { method: "POST" },
        );
        if (!res.ok) {
          setState(t("could not update the notes"), true);
          return;
        }
        setUpdating((await res.json()) as ScanProgress);
        window.dispatchEvent(new Event(SCAN_STARTED_EVENT));
        void watchScan();
      } catch {
        setState(t("could not update the notes"), true);
      }
    },
    [api, setState, watchScan],
  );

  // confirmDelete deletes what the person was asked about: the open note, or a folder of notes.
  const confirmDelete = useCallback(
    async (d: Deletion) => {
      setPending(null);
      if (d.kind === "note") {
        await deleteNote();
      } else {
        await deleteFolder(d.path, d.count);
      }
    },
    [deleteNote, deleteFolder],
  );

  const search = useCallback(
    async (q: string) => {
      setQuery(q);
      const seq = ++searchSeq.current;
      if (q.trim() === "") {
        setHits([]);
        return;
      }
      try {
        const res = await api(
          NOTES_PATH + "/search?q=" + encodeURIComponent(q),
        );
        const d = res.ok ? await res.json() : { hits: [] };
        if (seq === searchSeq.current) {
          setHits(d.hits);
        }
      } catch {
        if (seq === searchSeq.current) {
          setHits([]);
        }
      }
    },
    [api],
  );

  // followLink opens the note a [[link]] of the preview leads to, or makes it when it is new.
  const followLink = useCallback(
    async (href: string | null | undefined) => {
      const to = parseNoteHref(href);
      if (!to) {
        return;
      }
      if (to.kind === "note") {
        await openNote(to.path);
      } else {
        await createNote(folderOf(live.current.current?.path ?? ""), to.target);
      }
    },
    [openNote, createNote],
  );

  return {
    showNotes,
    notes,
    root,
    folders,
    updating,
    current,
    draft,
    setDraft,
    editing,
    setEditing,
    query,
    hits,
    graph,
    openGraph,
    closeGraph,
    openNotes,
    closeNotes,
    back,
    openNote,
    peekNote,
    saveNote,
    createNote,
    renameNote,
    deleteNote,
    deleteFolder,
    rescanFolder,
    pending,
    askDelete: setPending,
    confirmDelete,
    search,
    followLink,
  };
}
