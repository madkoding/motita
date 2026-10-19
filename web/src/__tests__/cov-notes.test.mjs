// The note vault on the page: the pure helpers (notesApi.ts), the hook (useNotes.ts) and the
// window (NotesModal.tsx), run together in jsdom against a fake gateway.
// Run with: node --test web/src/__tests__/cov-notes.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text } from "./cov-render-helpers.mjs";

// A stand-in for three.js: objects that remember what they were given, so the 3D map runs in jsdom.
const FAKE_THREE = `
export const AdditiveBlending = 2;
export class Vector3 {
  constructor() { this.x = 0; this.y = 0; this.z = 0; }
  set(x, y, z) { this.x = x; this.y = y; this.z = z; return this; }
  project() { return this; }
  length() { return Math.hypot(this.x, this.y, this.z); }
  setLength(d) { const k = d / this.length(); this.x *= k; this.y *= k; this.z *= k; return this; }
  lerp(v, a) { this.x += (v.x - this.x) * a; this.y += (v.y - this.y) * a; this.z += (v.z - this.z) * a; return this; }
}
export class Vector2 { constructor(x, y) { this.x = x; this.y = y; } }
class Obj {
  constructor(...args) {
    this.args = args;
    this.position = new Vector3();
    this.rotation = { x: 0, y: 0 };
    this.scale = { setScalar: (n) => { this.scaled = n; } };
    this.children = [];
  }
  add(...c) { this.children.push(...c); }
  traverse(fn) { fn(this); for (const c of this.children) { c.traverse(fn); } }
  updateMatrixWorld() {}
  getWorldPosition(v) {
    return v.set(this.position.x / 200, this.position.y / 200, globalThis.__threeDepth ?? 0.5);
  }
}
class Disposable { constructor(...args) { this.args = args; } dispose() { globalThis.__threeDisposed = (globalThis.__threeDisposed ?? 0) + 1; } setAttribute() {} }
export class BufferGeometry extends Disposable {}
export class SphereGeometry extends Disposable {}
export class Float32BufferAttribute extends Disposable {}
export class MeshBasicMaterial extends Disposable {}
export class LineBasicMaterial extends Disposable {}
export class PointsMaterial extends Disposable {}
export class FogExp2 extends Disposable {}
export class Mesh extends Obj { constructor(geometry, material) { super(); this.geometry = geometry; this.material = material; } }
export class Points extends Mesh {}
export class LineSegments extends Mesh {}
export class GridHelper extends Mesh { constructor() { super(new BufferGeometry(), new LineBasicMaterial()); } }
export class Group extends Obj {}
export class Scene extends Obj {}
export class PerspectiveCamera extends Obj { lookAt() {} updateProjectionMatrix() { this.updated = true; } }
export class WebGLRenderer {
  constructor() {
    if (globalThis.__threeFail) { throw new Error("no WebGL context"); }
    this.domElement = document.createElement("canvas");
    globalThis.__threeRenderer = this;
    this.frames = 0;
  }
  setPixelRatio(r) { this.ratio = r; }
  setSize(w, h) { this.size = [w, h]; }
  render() { this.frames++; }
  dispose() { this.disposed = true; }
}
`;
// The bloom passes, as stand-ins that count what they draw.
const FAKE_FX = (name) => `
export class ${name} {
  constructor(...args) { this.args = args; this.passes = []; globalThis.__fx = globalThis.__fx ?? {}; globalThis.__fx.${name} = this; }
  addPass(p) { this.passes.push(p); }
  render() { this.frames = (this.frames ?? 0) + 1; }
  setSize(w, h) { this.size = [w, h]; }
  dispose() { this.disposed = true; }
}
`;
const FX = "three/addons/postprocessing/";
const dom = setupDom({
  overrides: {
    three: FAKE_THREE,
    [FX + "EffectComposer.js"]: FAKE_FX("EffectComposer"),
    [FX + "RenderPass.js"]: FAKE_FX("RenderPass"),
    [FX + "UnrealBloomPass.js"]: FAKE_FX("UnrealBloomPass"),
    [FX + "OutputPass.js"]: FAKE_FX("OutputPass"),
  },
});
const W = dom.window;
globalThis.requestAnimationFrame = (cb) =>
  setTimeout(() => cb(performance.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const api = await import("../notesApi.ts");
const map3d = await import("../noteGraph3d.ts");
const realWebgl = map3d.graph3d.webgl;
map3d.graph3d.webgl = () => false;
const { useNotes } = await load("useNotes.ts");
const { NotesModal } = await load("NotesModal.tsx");
const { useReviewNotes, REVIEW_NOTES_PROMPT } = await load("useReviewNotes.ts");
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
  // scan.fill is what a first-use scan writes; absent, the gateway says it did not need to run.
  const scan = {};
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
    if (u.pathname === "/v1/notes/graph") {
      const paths = Object.keys(files).sort();
      const edges = [];
      for (const f of paths) {
        for (const l of view(f).links) {
          if (l.path && l.path !== f) {
            edges.push({ from: f, to: l.path });
          }
        }
      }
      return json({
        nodes: paths.map((p) => ({
          ...item(p),
          in: edges.filter((e) => e.to === p).length,
          out: edges.filter((e) => e.from === p).length,
        })),
        edges,
        missing: 1,
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
    if (u.pathname === "/v1/notes/scan") {
      if (scan.fill) {
        Object.assign(files, scan.fill);
      }
      return json({ ran: !!scan.fill });
    }
    if (u.pathname === "/v1/notes/rename") {
      const b = JSON.parse(init.body);
      if (files[b.to] !== undefined) {
        return json({ error: "exists" }, 409);
      }
      files[b.to] = files[b.from];
      delete files[b.from];
      return json({ updated: 0 });
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
  return { gw, files, calls, fail, scan };
}

async function mount(v, projects = []) {
  const statuses = [];
  const hook = { current: null };
  const review = { opened: "s1", sent: [], settings: 0 };
  function Harness() {
    const notes = useNotes(v.gw, (m, bad) => statuses.push([m, !!bad]));
    hook.current = notes;
    const reviewNotes = useReviewNotes(
      async () => review.opened,
      notes.closeNotes,
      (t) => review.sent.push(t),
      () => review.settings++,
    );
    return h(
      AppCtx.Provider,
      { value: { notes, reviewNotes, lists: { projects } } },
      notes.showNotes ? h(NotesModal, {}) : null,
    );
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(Harness, {}), root));
  return {
    root,
    hook,
    review,
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
  assert.match(text(q(m.root, "backlinks")), /Mentioned in \(1\)/);
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

test("a note is renamed in its folder, and a taken or empty name is refused", async () => {
  const v = vault({ ...FILES, "trips/Rome.md": "# Rome" });
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  const rows = () => qa(m.root, "note-row").map((b) => text(b));
  await click(qa(m.root, "note-row")[rows().indexOf("Rome")]);
  assert.equal(q(m.root, "note-rename-name"), null);
  await click(q(m.root, "note-rename"));
  assert.equal(q(m.root, "note-rename-name").value, "Rome");
  await click(q(m.root, "note-rename"));
  assert.equal(q(m.root, "note-rename-name"), null);

  const submit = async () => {
    await act(() =>
      q(m.root, "note-rename-go").dispatchEvent(
        new W.Event("submit", { bubbles: true, cancelable: true }),
      ),
    );
    await settle();
  };
  const typeName = async (value) => {
    await click(q(m.root, "note-rename"));
    await type(q(m.root, "note-rename-name"), value);
  };
  await typeName("  ");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["give the note a name", true]);
  await typeName("Rome");
  await submit();
  assert.equal(v.calls.filter((c) => c.url.includes("/rename")).length, 0);

  await typeName("Peru");
  await submit();
  assert.deepEqual(m.statuses.pop(), [
    "a note with that name already exists",
    true,
  ]);
  await typeName("Net");
  v.fail.add({ method: "POST", path: "/v1/notes/rename" });
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not rename the note", true]);
  v.fail.clear();
  v.fail.add({ method: "POST", path: "/v1/notes/rename", status: 500 });
  await typeName("Net");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not rename the note", true]);
  v.fail.clear();

  await typeName("Naples");
  await submit();
  assert.ok(v.files["trips/Naples.md"]);
  assert.equal(v.files["trips/Rome.md"], undefined);
  assert.equal(text(q(m.root, "note-place")), "tripsNaples");
  assert.equal(q(m.root, "note-rename-name"), null);
  await m.done();
});

test("renaming with no note open does nothing", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.renameNote("x"));
  assert.equal(v.calls.length, 0);
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
  assert.match(
    text(q(m.root, "backlinks")),
    /No other note mentions this one yet/,
  );
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

test("layoutGraph places every note inside the box, the same way every time", () => {
  const graph = {
    nodes: ["a", "b", "c", "d"].map((p) => ({
      path: p,
      title: p,
      in: 0,
      out: 0,
    })),
    edges: [
      { from: "a", to: "b" },
      { from: "b", to: "c" },
    ],
    missing: 0,
  };
  const one = api.layoutGraph(graph, 640, 440);
  const two = api.layoutGraph(graph, 640, 440);
  assert.deepEqual(one, two);
  for (const p of one) {
    assert.ok(p.x >= 28, p.path + " x min");
    assert.ok(p.x <= 612, p.path + " x max");
    assert.ok(p.y >= 28, p.path + " y min");
    assert.ok(p.y <= 412, p.path + " y max");
  }
  const d = (a, b) => Math.hypot(a.x - b.x, a.y - b.y);
  assert.ok(d(one[0], one[1]) < d(one[0], one[3]), "linked notes sit closer");
  // A single note sits alone and two notes at the same spot are pushed apart.
  assert.equal(
    api.layoutGraph({ ...graph, nodes: [graph.nodes[0]], edges: [] }, 640, 440)
      .length,
    1,
  );
  assert.deepEqual(
    api.layoutGraph({ nodes: [], edges: [], missing: 0 }, 640, 440),
    [],
  );
});

test("the graph shows the notes and their links, opens a note on a click, and toggles", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(qa(m.root, "note-row")[1]);
  await click(q(m.root, "note-graph-toggle"));
  assert.ok(q(m.root, "note-graph"));
  assert.equal(
    q(m.root, "note-mode"),
    null,
    "the open note is left for the graph",
  );
  assert.match(
    text(q(m.root, "note-graph")),
    /2 notes · 2 links · 1 to notes not written yet/,
  );
  assert.equal(qa(m.root, "graph-node").length, 2);
  assert.equal(q(m.root, "note-graph").querySelectorAll("line").length, 2);

  // Back to the list, then the toggle brings the graph again and closes it.
  await click(q(m.root, "graph-back"));
  assert.equal(q(m.root, "note-graph"), null);
  await click(q(m.root, "note-graph-toggle"));
  await click(q(m.root, "note-graph-toggle"));
  assert.equal(q(m.root, "note-graph"), null);

  // A click on a dot opens its note; so does Enter, and another key does nothing.
  await click(q(m.root, "note-graph-toggle"));
  const nodes = qa(m.root, "graph-node");
  await act(() =>
    nodes[0].dispatchEvent(
      new W.KeyboardEvent("keydown", { key: "a", bubbles: true }),
    ),
  );
  assert.ok(q(m.root, "note-graph"));
  await act(() =>
    nodes[0].dispatchEvent(
      new W.KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
    ),
  );
  await settle();
  assert.ok(q(m.root, "note-preview"));
  await click(q(m.root, "note-graph-toggle"));
  await click(qa(m.root, "graph-node")[1]);
  assert.ok(q(m.root, "note-preview"));
  assert.equal(q(m.root, "note-graph"), null);

  // Closing the window drops the graph too.
  await click(q(m.root, "note-graph-toggle"));
  await click(m.root.querySelector('button[aria-label="Close"]'));
  assert.equal(q(m.root, "note-graph"), null);
  await m.done();
});

test("an empty vault has nothing to draw, and a failing graph is said", async () => {
  const v = vault();
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(q(m.root, "note-graph-toggle"));
  assert.match(text(q(m.root, "note-graph")), /Nothing to draw yet/);
  await click(q(m.root, "note-graph-toggle"));

  v.fail.add({ method: "GET", path: "/graph", status: 500 });
  await click(q(m.root, "note-graph-toggle"));
  assert.deepEqual(m.statuses.pop(), ["could not load the graph", true]);
  v.fail.clear();
  v.fail.add({ method: "GET", path: "/graph" });
  await click(q(m.root, "note-graph-toggle"));
  assert.deepEqual(m.statuses.pop(), ["could not load the graph", true]);
  assert.equal(q(m.root, "note-graph"), null);

  // A draft that cannot be saved keeps the person in the note instead of the graph.
  v.fail.clear();
  const w = vault(FILES);
  const m2 = await mount(w);
  await m2.run((n) => n.openNotes());
  await click(qa(m2.root, "note-row")[0]);
  await click(q(m2.root, "note-mode"));
  await type(q(m2.root, "note-editor"), "changed");
  w.fail.add({ method: "PUT", path: "/v1/notes/note", status: 500 });
  await click(q(m2.root, "note-graph-toggle"));
  assert.equal(q(m2.root, "note-graph"), null);
  assert.ok(q(m2.root, "note-editor"));
  await m2.done();
  await m.done();
});

test("an empty vault is filled from the sessions and projects the first time it is opened", async () => {
  const v = vault();
  v.scan.fill = { "Sessions/First (s1).md": "# First" };
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await settle();
  assert.deepEqual(
    v.calls.filter((c) => c.url.includes("scan")).map((c) => c.url),
    ["/v1/notes/scan?first=1"],
  );
  assert.deepEqual(m.statuses.pop(), [
    "notes created from your sessions and projects",
    false,
  ]);
  assert.match(text(m.root), /First/);
  await m.done();
});

test("the first-use scan stays quiet when it is not needed or fails, and is not asked of a vault with notes", async () => {
  const idle = vault();
  let m = await mount(idle);
  await m.run((n) => n.openNotes());
  await settle();
  assert.equal(m.statuses.length, 0);
  assert.equal(idle.calls.filter((c) => c.url.includes("scan")).length, 1);
  await m.done();

  const broken = vault();
  broken.fail.add({ method: "POST", path: "/v1/notes/scan", status: 500 });
  m = await mount(broken);
  await m.run((n) => n.openNotes());
  await settle();
  assert.equal(m.statuses.length, 0);
  await m.done();

  const full = vault(FILES);
  m = await mount(full);
  await m.run((n) => n.openNotes());
  await settle();
  assert.equal(full.calls.filter((c) => c.url.includes("scan")).length, 0);
  await m.done();
});

test("Review closes the notes and sends the review request in a new conversation", async () => {
  const m = await mount(vault(FILES));
  await m.run((n) => n.openNotes());
  await click(q(m.root, "notes-review"));
  assert.deepEqual(m.review.sent, [REVIEW_NOTES_PROMPT]);
  assert.match(REVIEW_NOTES_PROMPT, /reviewing-the-notes/);
  assert.equal(q(m.root, "notes-review"), null, "the window closed");

  // No conversation could be opened: nothing is sent.
  m.review.opened = undefined;
  await m.run((n) => n.openNotes());
  await click(q(m.root, "notes-review"));
  assert.equal(m.review.sent.length, 1);
  await m.done();
});

test("Review stays put when the open note cannot be saved", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  await click(qa(m.root, "note-row")[0]);
  await click(q(m.root, "note-mode"));
  await type(q(m.root, "note-editor"), "changed");
  v.fail.add({ method: "PUT", path: "", status: 500 });
  await click(q(m.root, "notes-review"));
  assert.equal(m.review.sent.length, 0);
  assert.ok(q(m.root, "notes-review"), "the window is still open");
  await m.done();
});

test("the review also starts from an event, and closes the settings", async () => {
  const { REVIEW_NOTES_EVENT } = await load("useReviewNotes.ts");
  const m = await mount(vault(FILES));
  await act(() => window.dispatchEvent(new W.Event(REVIEW_NOTES_EVENT)));
  await settle();
  assert.deepEqual(m.review.sent, [REVIEW_NOTES_PROMPT]);
  assert.equal(m.review.settings, 1);
  await m.done();
  // Unmounted, the event no longer does anything.
  await act(() => window.dispatchEvent(new W.Event(REVIEW_NOTES_EVENT)));
  assert.equal(m.review.sent.length, 1);
});

// ---- the 3D map -------------------------------------------------------------------------------

test("layoutGraph3D spreads the notes in space the same way every time", () => {
  const graph = {
    nodes: ["a.md", "b.md", "c.md"].map((path) => ({
      path,
      title: path,
      size: 1,
      modified: "",
      in: 0,
      out: 0,
    })),
    edges: [{ from: "a.md", to: "b.md" }],
    missing: 0,
  };
  const one = api.layoutGraph3D(graph, 100);
  assert.deepEqual(one, api.layoutGraph3D(graph, 100));
  for (const p of one) {
    assert.ok(Math.hypot(p.x, p.y, p.z) <= 100.0001, "within the radius");
  }
  const ab = Math.hypot(one[0].x - one[1].x, one[0].y - one[1].y);
  const ac = Math.hypot(one[0].x - one[2].x, one[0].y - one[2].y);
  assert.ok(ab > 0 && ac > 0);
  assert.deepEqual(
    api.layoutGraph3D({ nodes: [], edges: [], missing: 0 }, 100),
    [],
  );
});

test("the reading helpers: a note's folders, its colour group and Start here first", () => {
  assert.equal(
    api.plainWiki("*It uses* [[P/M - Manual/a › b|B]] and [[P/Japan]], `x`."),
    "It uses B and Japan, x.",
  );
  assert.deepEqual(api.noteTrail("a/b/c.md"), ["a", "b"]);
  assert.deepEqual(api.noteTrail("c.md"), []);
  assert.equal(api.noteGroup("c.md", 6), 0);
  assert.equal(api.noteGroup("a/x.md", 6), api.noteGroup("a/y/z.md", 6));
  const n = (title) => ({ path: title + ".md", title, size: 0, modified: "" });
  assert.deepEqual(
    [n("b"), n("Start here"), n("a")]
      .sort(api.byReadingOrder)
      .map((x) => x.title),
    ["Start here", "a", "b"],
  );
});

test("WebGL is found where a canvas gives a context, and less motion is honoured", () => {
  const proto = W.HTMLCanvasElement.prototype;
  const real = proto.getContext;
  proto.getContext = (kind) => (kind === "webgl2" ? {} : null);
  assert.equal(realWebgl(), true);
  proto.getContext = (kind) => (kind === "webgl" ? {} : null);
  assert.equal(realWebgl(), true);
  proto.getContext = () => null;
  assert.equal(realWebgl(), false);
  proto.getContext = () => {
    throw new Error("blocked");
  };
  assert.equal(realWebgl(), false);
  proto.getContext = real;

  assert.equal(map3d.reducedMotion(), false);
  W.matchMedia = () => ({ matches: true });
  assert.equal(map3d.reducedMotion(), true);
  delete W.matchMedia;
});

// fakeHost is a box of a given size the map can draw into.
function fakeHost(w, h) {
  const el = document.createElement("div");
  Object.defineProperty(el, "clientWidth", { value: w });
  Object.defineProperty(el, "clientHeight", { value: h });
  document.body.appendChild(el);
  return el;
}

const GRAPH = {
  nodes: [
    { path: "a/one.md", title: "One", size: 1, modified: "", in: 2, out: 0 },
    { path: "two.md", title: "Two", size: 1, modified: "", in: 0, out: 1 },
  ],
  edges: [{ from: "two.md", to: "a/one.md" }],
  missing: 0,
};

test("the 3D map draws every note with its label, turns, zooms and cleans up", async () => {
  const T = await import("three");
  const host = fakeHost(400, 300);
  const opened = [];
  const map = map3d.mountGraph3D(T, host, GRAPH, (p) => opened.push(p), false);
  const r = globalThis.__threeRenderer;
  assert.deepEqual(r.size, [400, 300]);
  const labels = qa(host, "graph-node");
  assert.deepEqual(
    labels.map((b) => b.textContent),
    ["One", "Two"],
  );
  assert.equal(labels[0].style.visibility, "visible");
  labels[1].click();
  assert.deepEqual(opened, ["two.md"]);

  // Dragging turns it; a move without a press does nothing; the wheel stays within its limits.
  const at = (type, x, y) =>
    new W.MouseEvent(type, { clientX: x, clientY: y, bubbles: true });
  W.dispatchEvent(at("pointermove", 5, 5));
  host.dispatchEvent(at("pointerdown", 10, 10));
  W.dispatchEvent(at("pointermove", 60, 400));
  W.dispatchEvent(at("pointerup", 0, 0));
  const wheel = new W.WheelEvent("wheel", { deltaY: 5000, cancelable: true });
  host.dispatchEvent(wheel);
  assert.equal(wheel.defaultPrevented, true);
  host.dispatchEvent(new W.WheelEvent("wheel", { deltaY: -5000 }));
  W.dispatchEvent(new W.Event("resize"));

  // A note behind the camera hides its label.
  globalThis.__threeDepth = 2;
  await new Promise((res) => setTimeout(res, 5));
  assert.equal(labels[0].style.visibility, "hidden");
  globalThis.__threeDepth = undefined;
  assert.ok(r.frames > 1);

  map.stop();
  assert.equal(r.disposed, true);
  assert.ok(globalThis.__threeDisposed > 0);
  assert.equal(host.childNodes.length, 0);
  host.remove();

  // With the bloom, the composer draws; a search lights what it finds and turns to it.
  const fx = await map3d.graph3d.loadFx();
  const glowing = fakeHost(300, 200);
  const m3 = map3d.mountGraph3D(T, glowing, GRAPH, () => {}, false, fx);
  const composer = globalThis.__fx.EffectComposer;
  assert.equal(composer.passes.length, 3);
  assert.deepEqual(composer.size, [300, 200]);
  const glowLabels = qa(glowing, "graph-node");
  assert.equal(m3.find(" ONE "), 1);
  assert.deepEqual(
    glowLabels.map((b) => b.className),
    ["graph3d-label is-hit", "graph3d-label is-dim"],
  );
  assert.equal(m3.find("o"), 2);
  await new Promise((res) => setTimeout(res, 5));
  assert.ok(composer.frames > 0);
  assert.equal(m3.find(""), 0);
  assert.deepEqual(
    glowLabels.map((b) => b.className),
    ["graph3d-label", "graph3d-label"],
  );
  m3.stop();
  assert.equal(composer.disposed, true);
  glowing.remove();

  // With less motion asked for it stays still, and an empty box still draws.
  const still = fakeHost(0, 0);
  const m2 = map3d.mountGraph3D(T, still, GRAPH, () => {}, true);
  await new Promise((res) => setTimeout(res, 5));
  assert.deepEqual(globalThis.__threeRenderer.size, [1, 1]);
  m2.stop();
  still.remove();
});

test("the window shows the 3D map where WebGL works and the flat one where it fails", async () => {
  map3d.graph3d.webgl = () => true;
  const m = await mount(vault(FILES));
  await m.run((n) => n.openNotes());
  await click(q(m.root, "note-graph-toggle"));
  await settle();
  assert.ok(q(m.root, "graph-3d-hint"), "the 3D map is up");
  assert.equal(q(m.root, "note-graph").querySelector("svg"), null);
  assert.match(text(q(m.root, "note-graph-toggle")), /Close map/);
  const find = async (value) => {
    const input = q(m.root, "graph-search");
    input.value = value;
    await act(() =>
      input.dispatchEvent(new W.Event("input", { bubbles: true })),
    );
    await settle();
  };
  await find("peru");
  assert.equal(text(q(m.root, "graph-found")), "1 found");
  assert.deepEqual(
    qa(m.root, "graph-node").map((b) => b.className),
    ["graph3d-label is-dim", "graph3d-label is-hit"],
  );
  await find("");
  assert.equal(q(m.root, "graph-found"), null);
  await click(qa(m.root, "graph-node")[0]);
  assert.ok(q(m.root, "note-preview"), "a label opens its note");
  assert.equal(q(m.root, "note-graph"), null);

  // Closed before three.js arrives: nothing is drawn.
  const load = map3d.graph3d.load;
  let release;
  map3d.graph3d.load = () => new Promise((r) => (release = r));
  await click(q(m.root, "note-graph-toggle"));
  const before = globalThis.__threeRenderer;
  await click(q(m.root, "note-graph-toggle"));
  release(await import("three"));
  await settle();
  assert.equal(globalThis.__threeRenderer, before);
  map3d.graph3d.load = load;

  // No WebGL context after all: the flat map stays, and its search fades what it does not find.
  globalThis.__threeFail = true;
  await click(q(m.root, "note-graph-toggle"));
  await settle();
  assert.equal(q(m.root, "graph-3d-hint"), null);
  assert.ok(q(m.root, "note-graph").querySelector("svg"));
  await find("jap");
  assert.deepEqual(
    qa(m.root, "graph-node").map((g) => g.getAttribute("class")),
    [
      "cursor-pointer graph2d-node is-hit",
      "cursor-pointer graph2d-node is-dim",
    ],
  );
  await click(q(m.root, "note-graph-toggle"));
  globalThis.__threeFail = false;

  // Without the bloom the map still draws.
  const loadFx = map3d.graph3d.loadFx;
  map3d.graph3d.loadFx = () => Promise.reject(new Error("no chunk"));
  await click(q(m.root, "note-graph-toggle"));
  await settle();
  assert.ok(q(m.root, "graph-3d-hint"));
  map3d.graph3d.loadFx = loadFx;
  globalThis.__threeFail = false;
  await click(q(m.root, "note-graph-toggle"));
  await m.done();

  // An empty vault never asks for three.js.
  const e = await mount(vault());
  await e.run((n) => n.openNotes());
  await click(q(e.root, "note-graph-toggle"));
  assert.match(text(q(e.root, "note-graph")), /Nothing to draw yet/);
  await e.done();
  map3d.graph3d.webgl = () => false;
});

test("the list opens the folders at the top and the one holding the open note", async () => {
  const v = vault({
    "Projects/App - Manual/Start here.md": "# App manual",
    "Projects/App - Manual/web.md": "# Web",
    "Projects/deep/x.md": "x",
    "top.md": "# Top",
  });
  const m = await mount(v);
  await m.run((n) => n.openNotes());
  const folders = qa(m.root, "note-folder");
  assert.deepEqual(
    folders.map((f) => [f.querySelector("summary").textContent, f.open]),
    [
      ["Projects3", true],
      ["App - Manual2", false],
      ["deep1", false],
    ],
  );
  assert.deepEqual(
    qa(folders[1], "note-row").map((b) => text(b)),
    ["Start here", "web"],
  );
  await click(qa(folders[1], "note-row")[0]);
  assert.equal(qa(m.root, "note-folder")[1].open, true);
  assert.equal(
    text(q(m.root, "note-place")),
    "Projects › App - ManualStart here",
  );
  await m.done();
});
