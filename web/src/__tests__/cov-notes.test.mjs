// The note vault on the page: the pure helpers (notesApi.ts), the hook (useNotes.ts) and the
// window (NotesModal.tsx), run together in jsdom against a fake gateway.
// Run with: node --test web/src/__tests__/cov-notes.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text } from "./cov-render-helpers.mjs";

const dom = setupDom();
const W = dom.window;
globalThis.requestAnimationFrame = (cb) =>
  setTimeout(() => cb(performance.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const api = await import("../notesApi.ts");
const { useNotes } = await load("useNotes.ts");
const { NotesModal } = await load("NotesModal.tsx");
const { AppCtx } = await load("appStore.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const settle = async (n = 8) => {
  for (let i = 0; i < n; i++) {
    await act(() => new Promise((r) => setImmediate(r)));
  }
};
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// ---- the pure helpers ------------------------------------------------------------------------

test("buildTree groups notes into nested folders", () => {
  const n = (path) => ({ path, title: path, size: 1, modified: "" });
  const tree = api.buildTree([
    n("a.md"),
    n("trips/b.md"),
    n("trips/asia/c.md"),
    n("trips/d.md"),
  ]);
  assert.deepEqual(
    tree.notes.map((x) => x.path),
    ["a.md"],
  );
  assert.equal(tree.folders.length, 1);
  assert.equal(tree.folders[0].path, "trips");
  assert.deepEqual(
    tree.folders[0].notes.map((x) => x.path),
    ["trips/b.md", "trips/d.md"],
  );
  assert.equal(tree.folders[0].folders[0].path, "trips/asia");
});

test("folderOf and noteFileName", () => {
  assert.equal(api.folderOf("a/b/c.md"), "a/b");
  assert.equal(api.folderOf("c.md"), "");
  assert.equal(api.noteFileName("", "Idea"), "Idea.md");
  assert.equal(api.noteFileName("Proj", "Idea.MD"), "Proj/Idea.md");
  assert.equal(api.noteFileName("/Proj/", "sub / A:b?"), "Proj/sub/Ab.md");
  assert.equal(api.noteFileName("", "../.hid/x"), "x.md");
  for (const bad of ["", "  ", "..", ".md", "///"]) {
    assert.equal(api.noteFileName("Proj", bad), "", bad);
  }
});

test("wikiToMarkdown links resolved and new notes, and leaves code alone", () => {
  const links = [
    { target: "Japan", path: "trips/Japan.md" },
    { target: "ghost" },
  ];
  const body =
    "[[japan]] [[Japan#Food|the food]] [[ghost]] [[a b.md]] ![[Japan]]\n" +
    "`[[Japan]]` ```\n[[Japan]]\n``` [[ ]] [[|x]] [[Japan|[x]]]";
  const out = api.wikiToMarkdown(body, links);
  assert.match(out, /\[japan\]\(#note:trips%2FJapan\.md\)/);
  assert.match(out, /\[the food\]\(#note:trips%2FJapan\.md\)/);
  assert.match(out, /\[ghost\]\(#new:ghost\)/);
  assert.match(out, /\[a b\.md\]\(#new:a%20b\)/);
  assert.match(out, /`\[\[Japan\]\]` ```\n\[\[Japan\]\]\n```/);
  assert.match(out, /\[\[ \]\] \[\[\|x\]\]/);
});

test("parseNoteHref reads only its own links", () => {
  assert.deepEqual(api.parseNoteHref("#note:a%2Fb.md"), {
    kind: "note",
    path: "a/b.md",
  });
  assert.deepEqual(api.parseNoteHref("#new:a%20b"), {
    kind: "new",
    target: "a b",
  });
  assert.equal(api.parseNoteHref("https://x"), null);
  assert.equal(api.parseNoteHref(null), null);
});

// ---- the window, over the real hook ------------------------------------------------------------

// vault is a fake gateway holding notes in memory. `fail` makes a method+path throw or answer 500.
function vault(initial = {}) {
  const files = { ...initial };
  const calls = [];
  const fail = new Set();
  const item = (p) => ({
    path: p,
    title: p.split("/").pop().replace(/\.md$/, ""),
    size: files[p].length,
    modified: "",
  });
  const view = (p) => ({
    ...item(p),
    body: files[p],
    links: [...files[p].matchAll(/\[\[([^\]|#]+)/g)].map((m) => {
      const hit = Object.keys(files).find(
        (f) => f.replace(/\.md$/, "").split("/").pop() === m[1],
      );
      return hit ? { target: m[1], path: hit } : { target: m[1] };
    }),
    backlinks: Object.keys(files)
      .filter((f) => f !== p && files[f].includes("[[" + item(p).title))
      .map((f) => ({ ...item(f), context: files[f] })),
  });
  const gw = async (url, init = {}) => {
    const method = init.method || "GET";
    calls.push({ method, url, body: init.body });
    for (const f of fail) {
      if (url.includes(f.path) && method === f.method) {
        if (f.status) {
          return json({ error: "no" }, f.status);
        }
        throw new Error("network");
      }
    }
    const u = new URL(url, "http://x");
    const path = u.searchParams.get("path");
    if (u.pathname === "/v1/notes" && method === "GET") {
      return json({
        root: "/home/me/.motita/notes",
        notes: Object.keys(files).sort().map(item),
      });
    }
    if (u.pathname === "/v1/notes/search") {
      const q = u.searchParams.get("q");
      return json({
        hits: Object.keys(files)
          .filter((f) => files[f].includes(q))
          .map((f) => ({ path: f, title: f, line: 1, snippet: files[f] })),
      });
    }
    if (method === "GET") {
      return files[path] === undefined
        ? json({ error: "x" }, 404)
        : json(view(path));
    }
    if (method === "PUT") {
      const b = JSON.parse(init.body);
      if (b.create && files[path] !== undefined) {
        return json({ error: "exists" }, 409);
      }
      files[path] = b.body;
      return json(item(path));
    }
    delete files[path];
    return new Response(null, { status: 204 });
  };
  return { gw, files, calls, fail };
}

async function mount(v, projects = []) {
  const statuses = [];
  const hook = { current: null };
  function Harness() {
    const notes = useNotes(v.gw, (m, bad) => statuses.push([m, !!bad]));
    hook.current = notes;
    return h(
      AppCtx.Provider,
      { value: { notes, lists: { projects } } },
      notes.showNotes ? h(NotesModal, {}) : null,
    );
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(Harness, {}), root));
  return {
    root,
    hook,
    statuses,
    run: async (fn) => {
      await act(() => fn(hook.current));
      await settle();
    },
    async done() {
      await act(() => render(null, root));
      root.remove();
    },
  };
}

const click = async (el) => {
  await act(() => {
    el.dispatchEvent(
      new W.MouseEvent("click", { bubbles: true, cancelable: true }),
    );
  });
  await settle();
};
const type = async (el, value) => {
  el.value = value;
  await act(() => {
    el.dispatchEvent(new W.Event("input", { bubbles: true }));
  });
  await settle();
};
const q = (root, id) => root.querySelector(`[data-testid="${id}"]`);
const qa = (root, id) => [...root.querySelectorAll(`[data-testid="${id}"]`)];

const FILES = {
  "Japan.md": "# Japan\nSee [[Peru]] and [[Nowhere]]",
  "trips/Peru.md": "Back to [[Japan]]",
};

test("the window lists the notes as a tree, with an empty state and the vault folder", async () => {
  const empty = await mount(vault());
  await empty.run((n) => n.openNotes());
  assert.match(text(empty.root), /No notes yet/);
  assert.match(text(empty.root), /Pick a note, or write a new one/);
  assert.match(text(empty.root), /\/home\/me\/\.motita\/notes/);
  await empty.done();

  const m = await mount(vault(FILES));
  await m.run((n) => n.openNotes());
  assert.equal(qa(m.root, "note-folder").length, 1);
  assert.deepEqual(
    qa(m.root, "note-row").map((b) => text(b)),
    ["Peru", "Japan"],
  );
  await m.done();
});

test("a note opens as a preview whose [[links]] open the note, or make it when new", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(qa(m.root, "note-row").find((b) => text(b) === "Japan"));
  assert.match(text(q(m.root, "note-preview")), /See Peru and Nowhere/);
  assert.equal(q(m.root, "note-back") !== null, true);
  assert.match(text(q(m.root, "backlinks")), /Linked from \(1\)/);
  assert.match(text(q(m.root, "backlinks")), /Peru/);

  // A click that is not on a note link does nothing; one on [[Peru]] opens Peru.
  await click(q(m.root, "note-preview"));
  const anchors = [...q(m.root, "note-preview").querySelectorAll("a")];
  assert.deepEqual(
    anchors.map((a) => a.getAttribute("href")),
    ["#note:trips%2FPeru.md", "#new:Nowhere"],
  );
  const other = document.createElement("a");
  other.setAttribute("href", "https://example.com");
  q(m.root, "note-preview").appendChild(other);
  await click(other);
  assert.match(text(q(m.root, "note-preview")), /See Peru/);
  await click(anchors[0]);
  assert.match(text(q(m.root, "note-preview")), /Back to Japan/);
  assert.match(text(q(m.root, "backlinks")), /Japan/);

  // The back link from Peru, then the note that does not exist yet is created next to this one.
  await click(q(m.root, "backlink"));
  await click(
    [...q(m.root, "note-preview").querySelectorAll("a")].find(
      (a) => a.getAttribute("href") === "#new:Nowhere",
    ),
  );
  assert.equal(v.files["Nowhere.md"], "# Nowhere\n\n");
  assert.ok(q(m.root, "note-editor"));
  await m.done();
});

test("a new link made from a note in a folder is created in that folder", async () => {
  const v = vault({ "trips/Peru.md": "[[Cusco]]" });
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(q(m.root, "note-row"));
  await click(q(m.root, "note-preview").querySelector("a"));
  assert.equal(v.files["trips/Cusco.md"], "# Cusco\n\n");
  await m.done();
});

test("editing keeps a draft, saves on request, on moving on and on closing", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  const row = (name) => qa(m.root, "note-row").find((b) => text(b) === name);
  await click(row("Japan"));
  assert.equal(q(m.root, "note-save"), null);
  await click(q(m.root, "note-mode"));
  assert.match(text(q(m.root, "note-mode")), /Preview/);
  await type(q(m.root, "note-editor"), "# Japan\nhello [[Peru]]");
  assert.ok(q(m.root, "note-save"));
  await click(q(m.root, "note-save"));
  assert.equal(v.files["Japan.md"], "# Japan\nhello [[Peru]]");
  assert.equal(q(m.root, "note-save"), null);
  assert.ok(q(m.root, "note-editor"), "still editing after a save");

  // Leaving a dirty note for another one saves it first.
  await type(q(m.root, "note-editor"), "second draft");
  await click(row("Peru"));
  assert.equal(v.files["Japan.md"], "second draft");
  // Closing saves too.
  await click(q(m.root, "note-mode"));
  await type(q(m.root, "note-editor"), "third");
  await click(m.root.querySelector('button[aria-label="Close"]'));
  assert.equal(v.files["trips/Peru.md"], "third");
  assert.equal(q(m.root, "note-search"), null);
  await m.done();
});

test("a save that fails keeps the person where they are", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(qa(m.root, "note-row")[1]);
  await click(q(m.root, "note-mode"));
  await type(q(m.root, "note-editor"), "changed");

  v.fail.add({ method: "PUT", path: "/v1/notes/note", status: 500 });
  await click(q(m.root, "note-save"));
  await click(q(m.root, "note-back"));
  await click(m.root.querySelector('button[aria-label="Close"]'));
  await click(qa(m.root, "note-row")[0]);
  assert.ok(q(m.root, "note-editor"), "the draft is still on screen");
  assert.equal(q(m.root, "note-editor").value, "changed");
  assert.ok(m.statuses.every(([s, bad]) => bad && /save/.test(s)));
  assert.ok(m.statuses.length >= 4);

  // Making a note keeps the draft too when it cannot be saved first.
  v.fail.add({ method: "PUT", path: "/v1/notes/note", status: 500 });
  const before = Object.keys(v.files).length;
  await m.run((n) => n.createNote("", "Other"));
  assert.equal(Object.keys(v.files).length, before);

  v.fail.clear();
  v.fail.add({ method: "PUT", path: "/v1/notes/note" });
  m.statuses.length = 0;
  await click(q(m.root, "note-save"));
  assert.deepEqual(m.statuses, [["could not save the note", true]]);
  await m.done();
});

test("the back button leaves the note for the list", async () => {
  const m = await mount(vault(FILES));
  await m.run((n) => n.openNotes());
  await click(qa(m.root, "note-row")[0]);
  await click(q(m.root, "note-back"));
  assert.equal(q(m.root, "note-mode"), null);
  assert.match(text(m.root), /Pick a note/);
  await m.done();
});

test("a note is created at the top level, in a project's folder, and refused when it exists", async () => {
  const v = vault(FILES);
  const m = await mount(v, [{ id: "p1", title: "Motita" }]);
  await m.run((n) => n.openNotes());
  const submit = async () => {
    await act(() => {
      q(m.root, "note-create").dispatchEvent(
        new W.MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    await settle();
  };
  await submit();
  assert.deepEqual(m.statuses.pop(), ["give the note a name", true]);

  await type(q(m.root, "note-new-name"), "Idea");
  await submit();
  assert.equal(v.files["Idea.md"], "# Idea\n\n");
  assert.equal(q(m.root, "note-new-name").value, "");
  assert.ok(q(m.root, "note-editor"));

  const folder = q(m.root, "note-new-folder");
  folder.value = "Motita";
  await act(() =>
    folder.dispatchEvent(new W.Event("change", { bubbles: true })),
  );
  await type(q(m.root, "note-new-name"), "Plan");
  await submit();
  assert.equal(v.files["Motita/Plan.md"], "# Plan\n\n");

  await type(q(m.root, "note-new-name"), "Idea");
  folder.value = "";
  await act(() =>
    folder.dispatchEvent(new W.Event("change", { bubbles: true })),
  );
  await submit();
  assert.deepEqual(m.statuses.pop(), [
    "a note with that name already exists",
    true,
  ]);

  v.fail.add({ method: "PUT", path: "Broken.md", status: 500 });
  await type(q(m.root, "note-new-name"), "Broken");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not create the note", true]);
  v.fail.clear();
  v.fail.add({ method: "PUT", path: "Net.md" });
  await type(q(m.root, "note-new-name"), "Net");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not create the note", true]);
  await m.done();
});

test("a note is deleted after a second click, and the first one disarms on blur", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(qa(m.root, "note-row")[1]);
  await click(q(m.root, "note-delete"));
  assert.match(text(q(m.root, "note-delete")), /Click again to delete/);
  await act(() =>
    q(m.root, "note-delete").dispatchEvent(
      new W.FocusEvent("focusout", { bubbles: true }),
    ),
  );
  assert.match(text(q(m.root, "note-delete")), /^Delete$/);
  await click(q(m.root, "note-delete"));
  await click(q(m.root, "note-delete"));
  assert.equal(v.files["Japan.md"], undefined);
  assert.equal(q(m.root, "note-mode"), null);
  assert.deepEqual(
    qa(m.root, "note-row").map((b) => text(b)),
    ["Peru"],
  );

  // A failed delete leaves the note and says so.
  await click(qa(m.root, "note-row")[0]);
  v.fail.add({ method: "DELETE", path: "/v1/notes/note", status: 500 });
  await click(q(m.root, "note-delete"));
  await click(q(m.root, "note-delete"));
  assert.deepEqual(m.statuses.pop(), ["could not delete the note", true]);
  v.fail.clear();
  v.fail.add({ method: "DELETE", path: "/v1/notes/note" });
  await click(q(m.root, "note-delete"));
  await click(q(m.root, "note-delete"));
  assert.deepEqual(m.statuses.pop(), ["could not delete the note", true]);
  await m.done();
});

test("deleting with no note open does nothing", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.deleteNote());
  await m.run((n) => n.saveNote());
  assert.equal(v.calls.length, 0);
  await m.done();
});

test("search shows the passages, then the tree again when cleared; the newest answer wins", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await type(q(m.root, "note-search"), "Peru");
  assert.deepEqual(
    qa(m.root, "note-hit").map((b) => text(b).slice(0, 8)),
    ["Japan.md"],
  );
  await click(q(m.root, "note-hit"));
  assert.match(text(q(m.root, "note-preview")), /See Peru/);
  await type(q(m.root, "note-search"), "zzz");
  assert.match(text(m.root), /Nothing matches that/);
  await type(q(m.root, "note-search"), "  ");
  assert.equal(qa(m.root, "note-row").length, 2);

  // A slow answer for an old query does not replace a newer one.
  let release;
  const slow = new Promise((r) => (release = r));
  const real = v.gw;
  let n = 0;
  const m2 = await mount({
    ...v,
    gw: async (url, init) => {
      if (url.includes("/search") && n++ === 0) {
        await slow;
      }
      return real(url, init);
    },
  });
  await m2.run((x) => x.openNotes());
  await act(() => {
    void m2.hook.current.search("Peru");
    void m2.hook.current.search("Back");
  });
  await settle();
  release();
  await settle();
  assert.deepEqual(
    m2.hook.current.hits.map((x) => x.path),
    ["trips/Peru.md"],
  );
  await m2.done();
  await m.done();
});

test("search failures show no hits", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  v.fail.add({ method: "GET", path: "/search", status: 500 });
  await type(q(m.root, "note-search"), "Peru");
  assert.equal(qa(m.root, "note-hit").length, 0);
  v.fail.clear();
  v.fail.add({ method: "GET", path: "/search" });
  await type(q(m.root, "note-search"), "Peru!");
  assert.equal(qa(m.root, "note-hit").length, 0);
  // A network failure of an outdated search is dropped quietly too.
  await act(() => {
    void m.hook.current.search("a");
    void m.hook.current.search("");
  });
  await settle();
  await m.done();
});

test("loading and opening failures are said, not thrown", async () => {
  const v = vault(FILES);
  v.fail.add({ method: "GET", path: "/v1/notes", status: 500 });
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  assert.deepEqual(m.statuses, []);
  v.fail.clear();
  v.fail.add({ method: "GET", path: "/v1/notes" });
  await m.run((n) => n.openNotes());
  assert.deepEqual(m.statuses.pop(), ["could not load the notes", true]);
  v.fail.clear();
  await m.run((n) => n.openNotes());
  await m.run((n) => n.openNote("missing.md"));
  assert.deepEqual(m.statuses.pop(), ["could not open the note", true]);
  v.fail.add({ method: "GET", path: "Japan.md" });
  await m.run((n) => n.openNote("Japan.md"));
  assert.deepEqual(m.statuses.pop(), ["could not open the note", true]);
  await m.done();
});

test("a link to nothing opens nothing, and a plain close with no note works", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await m.run((n) => n.followLink("https://example.com"));
  await m.run((n) => n.followLink(null));
  assert.equal(q(m.root, "note-mode"), null);
  // A new link with no note open goes to the top level.
  await m.run((n) => n.followLink("#new:Loose"));
  assert.equal(v.files["Loose.md"], "# Loose\n\n");
  await m.done();
});

test("the preview of a note without links still draws", async () => {
  const m = await mount(vault({ "a.md": "plain" }));
  await m.run((n) => n.openNotes());
  await click(q(m.root, "note-row"));
  assert.match(text(q(m.root, "note-preview")), /plain/);
  assert.match(text(q(m.root, "backlinks")), /No other note links here yet/);
  await m.done();
});

test("every string of the notes window has its Spanish", async () => {
  const fs = await import("node:fs");
  for (const f of ["NotesModal.tsx", "useNotes.ts"]) {
    const src = fs.readFileSync(new URL("../" + f, import.meta.url), "utf8");
    for (const m of src.matchAll(/\bt[f]?\(\s*"((?:[^"\\]|\\.)*)"/g)) {
      assert.ok(es[m[1]], `missing Spanish for: ${m[1]}`);
    }
  }
});
