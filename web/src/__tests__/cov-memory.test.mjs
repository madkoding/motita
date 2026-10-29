// The note vault on the page: the pure helpers (notesApi.ts), the hook (useMemory.ts) and the
// window (MemoryModal.tsx), run together in jsdom against a fake gateway.
// Run with: node --test web/src/__tests__/cov-notes.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text, waitFor } from "./cov-render-helpers.mjs";

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
const dom = setupDom({ overrides: { three: FAKE_THREE } });
const W = dom.window;
globalThis.requestAnimationFrame = (cb) =>
  setTimeout(() => cb(performance.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const api = await import("../memoryApi.ts");
const map3d = await import("../memoryGraph3d.ts");
const realWebgl = map3d.graph3d.webgl;
map3d.graph3d.webgl = () => false;
const { useMemory } = await load("useMemory.ts");
const { MemoryModal } = await load("MemoryModal.tsx");
const { useReviewMemory, REVIEW_MEMORY_PROMPT } =
  await load("useReviewMemory.ts");
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
    tree.entries.map((x) => x.path),
    ["a.md"],
  );
  assert.equal(tree.folders.length, 1);
  assert.equal(tree.folders[0].path, "trips");
  assert.deepEqual(
    tree.folders[0].entries.map((x) => x.path),
    ["trips/b.md", "trips/d.md"],
  );
  assert.equal(tree.folders[0].folders[0].path, "trips/asia");
});

test("folderOf and entryFileName", () => {
  assert.equal(api.folderOf("a/b/c.md"), "a/b");
  assert.equal(api.folderOf("c.md"), "");
  assert.equal(api.entryFileName("", "Idea"), "Idea.md");
  assert.equal(api.entryFileName("Proj", "Idea.MD"), "Proj/Idea.md");
  assert.equal(api.entryFileName("/Proj/", "sub / A:b?"), "Proj/sub/Ab.md");
  assert.equal(api.entryFileName("", "../.hid/x"), "x.md");
  for (const bad of ["", "  ", "..", ".md", "///"]) {
    assert.equal(api.entryFileName("Proj", bad), "", bad);
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
  assert.match(out, /\[japan\]\(#entry:trips%2FJapan\.md\)/);
  assert.match(out, /\[the food\]\(#entry:trips%2FJapan\.md\)/);
  assert.match(out, /\[ghost\]\(#new:ghost\)/);
  assert.match(out, /\[a b\.md\]\(#new:a%20b\)/);
  assert.match(out, /`\[\[Japan\]\]` ```\n\[\[Japan\]\]\n```/);
  assert.match(out, /\[\[ \]\] \[\[\|x\]\]/);
});

test("parseEntryHref reads only its own links", () => {
  assert.deepEqual(api.parseEntryHref("#entry:a%2Fb.md"), {
    kind: "entry",
    path: "a/b.md",
  });
  assert.deepEqual(api.parseEntryHref("#new:a%20b"), {
    kind: "new",
    target: "a b",
  });
  assert.equal(api.parseEntryHref("https://x"), null);
  assert.equal(api.parseEntryHref(null), null);
});

// ---- the window, over the real hook ------------------------------------------------------------

// vault is a fake gateway holding notes in memory. `fail` makes a method+path throw or answer 500.
function vault(initial = {}) {
  const files = { ...initial };
  const calls = [];
  const fail = new Set();
  const state = {};
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
    if (u.pathname === "/v1/memory" && method === "GET") {
      return json({
        root: state.root ?? "/home/me/.motita/memory",
        entries: Object.keys(files).sort().map(item),
        namespaces: state.namespaces,
      });
    }
    if (u.pathname === "/v1/memory/folder" && method === "DELETE") {
      const gone = Object.keys(files).filter((f) => f.startsWith(path + "/"));
      gone.forEach((f) => delete files[f]);
      return json({ deleted: gone.length });
    }
    if (u.pathname === "/v1/memory/graph") {
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
    if (u.pathname === "/v1/memory/search") {
      const q = u.searchParams.get("q");
      return json({
        hits: Object.keys(files)
          .filter((f) => files[f].includes(q))
          .map((f) => ({
            path: f,
            title: f,
            namespace: f.includes("/") ? f.split("/")[0] : "",
            line: 1,
            snippet: files[f],
            block: files[f],
            date: "",
            score: 1,
          })),
      });
    }
    if (u.pathname === "/v1/memory/rename") {
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
  return { gw, files, calls, fail, state };
}

async function mount(v, projects = []) {
  const statuses = [];
  const hook = { current: null };
  const review = { opened: "s1", sent: [], settings: 0 };
  function Harness() {
    const memory = useMemory(v.gw, (m, bad) => statuses.push([m, !!bad]));
    hook.current = memory;
    const reviewMemory = useReviewMemory(
      async () => review.opened,
      memory.closeMemory,
      (t) => review.sent.push(t),
      () => review.settings++,
    );
    return h(
      AppCtx.Provider,
      { value: { memory, reviewMemory, lists: { projects } } },
      memory.showMemory ? h(MemoryModal, {}) : null,
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
// Menus and the question before a deletion are drawn at the end of <body>, outside the window.
const qd = (id) => document.querySelector(`[data-testid="${id}"]`);
// pick opens the "⋯" menu whose button is at menuId (the first, or the nth) and chooses its item.
const pick = async (root, menuId, itemId, nth = 0) => {
  await click(qa(root, menuId)[nth]);
  await click(qd(itemId));
};
const q = (root, id) => root.querySelector(`[data-testid="${id}"]`);
const qa = (root, id) => [...root.querySelectorAll(`[data-testid="${id}"]`)];

const FILES = {
  "Japan.md": "# Japan\nSee [[Peru]] and [[Nowhere]]",
  "trips/Peru.md": "Back to [[Japan]]",
};

test("the window lists the notes as a tree, with an empty state and the vault folder", async () => {
  const empty = await mount(vault());
  await empty.run((n) => n.openMemory());
  assert.match(text(empty.root), /Nothing in the memory yet/);
  assert.match(text(empty.root), /Welcome to your memory/);
  assert.match(text(empty.root), /\/home\/me\/\.motita\/memory/);
  await empty.done();

  const m = await mount(vault(FILES));
  await m.run((n) => n.openMemory());
  assert.equal(qa(m.root, "entry-folder").length, 1);
  assert.deepEqual(
    qa(m.root, "entry-row").map((b) => text(b)),
    ["Peru", "Japan"],
  );
  await m.done();
});

test("a note opens as a preview whose [[links]] open the note, or make it when new", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(qa(m.root, "entry-row").find((b) => text(b) === "Japan"));
  assert.match(text(q(m.root, "entry-preview")), /See Peru and Nowhere/);
  assert.equal(q(m.root, "entry-back") !== null, true);
  assert.match(text(q(m.root, "backlinks")), /Pages that link here \(1\)/);
  assert.match(text(q(m.root, "backlinks")), /Peru/);

  // A click that is not on a note link does nothing; one on [[Peru]] opens Peru.
  await click(q(m.root, "entry-preview"));
  const anchors = [...q(m.root, "entry-preview").querySelectorAll("a")];
  assert.deepEqual(
    anchors.map((a) => a.getAttribute("href")),
    ["#entry:trips%2FPeru.md", "#new:Nowhere"],
  );
  const other = document.createElement("a");
  other.setAttribute("href", "https://example.com");
  q(m.root, "entry-preview").appendChild(other);
  await click(other);
  assert.match(text(q(m.root, "entry-preview")), /See Peru/);
  // Enter on a link follows it; any other key does not.
  const key = (k) =>
    act(() =>
      anchors[0].dispatchEvent(
        new W.KeyboardEvent("keydown", { key: k, bubbles: true }),
      ),
    );
  await key("a");
  assert.match(text(q(m.root, "entry-preview")), /See Peru/);
  await key("Enter");
  await settle();
  assert.match(text(q(m.root, "entry-preview")), /Back to Japan/);
  assert.match(text(q(m.root, "backlinks")), /Japan/);

  // The back link from Peru, then the note that does not exist yet is created next to this one.
  await click(q(m.root, "backlink"));
  await click(
    [...q(m.root, "entry-preview").querySelectorAll("a")].find(
      (a) => a.getAttribute("href") === "#new:Nowhere",
    ),
  );
  assert.equal(v.files["Nowhere.md"], "# Nowhere\n\n");
  assert.ok(q(m.root, "entry-editor"));
  await m.done();
});

test("a new link made from a note in a folder is created in that folder", async () => {
  const v = vault({ "trips/Peru.md": "[[Cusco]]" });
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(q(m.root, "entry-row"));
  await click(q(m.root, "entry-preview").querySelector("a"));
  assert.equal(v.files["trips/Cusco.md"], "# Cusco\n\n");
  await m.done();
});

test("editing keeps a draft, saves on request, on moving on and on closing", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  const row = (name) => qa(m.root, "entry-row").find((b) => text(b) === name);
  await click(row("Japan"));
  assert.equal(q(m.root, "entry-save"), null);
  await click(q(m.root, "entry-mode"));
  assert.match(text(q(m.root, "entry-mode")), /Preview/);
  await type(q(m.root, "entry-editor"), "# Japan\nhello [[Peru]]");
  assert.ok(q(m.root, "entry-save"));
  await click(q(m.root, "entry-save"));
  assert.equal(v.files["Japan.md"], "# Japan\nhello [[Peru]]");
  assert.equal(q(m.root, "entry-save"), null);
  assert.ok(q(m.root, "entry-editor"), "still editing after a save");

  // Leaving a dirty note for another one saves it first.
  await type(q(m.root, "entry-editor"), "second draft");
  await click(row("Peru"));
  assert.equal(v.files["Japan.md"], "second draft");
  // Closing saves too.
  await click(q(m.root, "entry-mode"));
  await type(q(m.root, "entry-editor"), "third");
  await click(m.root.querySelector('button[aria-label="Close"]'));
  assert.equal(v.files["trips/Peru.md"], "third");
  assert.equal(q(m.root, "memory-search"), null);
  await m.done();
});

test("a save that fails keeps the person where they are", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(qa(m.root, "entry-row")[1]);
  await click(q(m.root, "entry-mode"));
  await type(q(m.root, "entry-editor"), "changed");

  v.fail.add({ method: "PUT", path: "/v1/memory/entry", status: 500 });
  await click(q(m.root, "entry-save"));
  await click(q(m.root, "entry-back"));
  await click(m.root.querySelector('button[aria-label="Close"]'));
  await click(qa(m.root, "entry-row")[0]);
  assert.ok(q(m.root, "entry-editor"), "the draft is still on screen");
  assert.equal(q(m.root, "entry-editor").value, "changed");
  assert.ok(m.statuses.every(([s, bad]) => bad && /save/.test(s)));
  assert.ok(m.statuses.length >= 4);

  // Making a note keeps the draft too when it cannot be saved first.
  v.fail.add({ method: "PUT", path: "/v1/memory/entry", status: 500 });
  const before = Object.keys(v.files).length;
  await m.run((n) => n.createEntry("", "Other"));
  assert.equal(Object.keys(v.files).length, before);

  v.fail.clear();
  v.fail.add({ method: "PUT", path: "/v1/memory/entry" });
  m.statuses.length = 0;
  await click(q(m.root, "entry-save"));
  assert.deepEqual(m.statuses, [["could not save the entry", true]]);
  await m.done();
});

test("the back button leaves the note for the list", async () => {
  const m = await mount(vault(FILES));
  await m.run((n) => n.openMemory());
  await click(qa(m.root, "entry-row")[0]);
  await click(q(m.root, "entry-back"));
  assert.equal(q(m.root, "entry-mode"), null);
  assert.match(text(m.root), /Welcome to your memory/);
  await m.done();
});

test("a note is created at the top level, in a project's folder, and refused when it exists", async () => {
  const v = vault(FILES);
  const m = await mount(v, [{ id: "p1", title: "Motita" }]);
  await m.run((n) => n.openMemory());
  const submit = async () => {
    await act(() => {
      q(m.root, "entry-create").dispatchEvent(
        new W.MouseEvent("click", { bubbles: true, cancelable: true }),
      );
    });
    await settle();
  };
  // The form opens from its button and closes once used.
  const form = async () => {
    if (!q(m.root, "entry-create")) {
      await click(q(m.root, "entry-new-toggle"));
    }
  };
  assert.equal(q(m.root, "entry-create"), null);
  await form();
  assert.equal(
    q(m.root, "entry-new-toggle").getAttribute("aria-expanded"),
    "true",
  );
  await submit();
  assert.deepEqual(m.statuses.pop(), ["give the entry a name", true]);
  assert.equal(q(m.root, "entry-create"), null);

  await form();
  await type(q(m.root, "entry-new-name"), "Idea");
  await submit();
  assert.equal(v.files["Idea.md"], "# Idea\n\n");
  assert.equal(q(m.root, "entry-new-name"), null, "the form closed");
  assert.ok(q(m.root, "entry-editor"));

  await form();
  await type(q(m.root, "entry-new-folder"), "Motita");
  await type(q(m.root, "entry-new-name"), "Plan");
  await submit();
  assert.equal(v.files["Motita/Plan.md"], "# Plan\n\n");

  await form();
  await type(q(m.root, "entry-new-name"), "Idea");
  await type(q(m.root, "entry-new-folder"), "");
  await submit();
  assert.deepEqual(m.statuses.pop(), [
    "an entry with that name already exists",
    true,
  ]);

  v.fail.add({ method: "PUT", path: "Broken.md", status: 500 });
  await form();
  await type(q(m.root, "entry-new-name"), "Broken");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not create the entry", true]);
  v.fail.clear();
  v.fail.add({ method: "PUT", path: "Net.md" });
  await form();
  await type(q(m.root, "entry-new-name"), "Net");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not create the entry", true]);
  await m.done();
});

test("a note is deleted after the person confirms, and not when they keep it", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(qa(m.root, "entry-row")[1]);
  await pick(m.root, "entry-menu", "entry-delete");
  assert.match(text(qd("entry-confirm")), /Delete this entry\?/);
  assert.match(text(qd("entry-confirm")), /Japan/);
  assert.equal(qd("entry-confirm-count"), null);
  assert.match(text(qd("entry-confirm-yes")), /Delete the entry/);
  await click(qd("entry-confirm-no"));
  assert.equal(qd("entry-confirm"), null);
  assert.ok(v.files["Japan.md"]);

  await pick(m.root, "entry-menu", "entry-delete");
  await click(qd("entry-confirm-yes"));
  assert.equal(v.files["Japan.md"], undefined);
  assert.equal(q(m.root, "entry-mode"), null);
  assert.deepEqual(
    qa(m.root, "entry-row").map((b) => text(b)),
    ["Peru"],
  );

  // A failed delete leaves the note and says so.
  await click(qa(m.root, "entry-row")[0]);
  v.fail.add({ method: "DELETE", path: "/v1/memory/entry", status: 500 });
  await pick(m.root, "entry-menu", "entry-delete");
  await click(qd("entry-confirm-yes"));
  assert.deepEqual(m.statuses.pop(), ["could not delete the entry", true]);
  v.fail.clear();
  v.fail.add({ method: "DELETE", path: "/v1/memory/entry" });
  await pick(m.root, "entry-menu", "entry-delete");
  await click(qd("entry-confirm-yes"));
  assert.deepEqual(m.statuses.pop(), ["could not delete the entry", true]);
  await m.done();
});

test("a note is renamed in its folder, and a taken or empty name is refused", async () => {
  const v = vault({ ...FILES, "trips/Rome.md": "# Rome" });
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  const rows = () => qa(m.root, "entry-row").map((b) => text(b));
  await click(qa(m.root, "entry-row")[rows().indexOf("Rome")]);
  assert.equal(q(m.root, "entry-rename-name"), null);
  await pick(m.root, "entry-menu", "entry-rename");
  assert.equal(q(m.root, "entry-rename-name").value, "Rome");

  const submit = async () => {
    await act(() =>
      q(m.root, "entry-rename-go").dispatchEvent(
        new W.Event("submit", { bubbles: true, cancelable: true }),
      ),
    );
    await settle();
  };
  const typeName = async (value) => {
    await pick(m.root, "entry-menu", "entry-rename");
    await type(q(m.root, "entry-rename-name"), value);
  };
  await typeName("  ");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["give the entry a name", true]);
  await typeName("Rome");
  await submit();
  assert.equal(v.calls.filter((c) => c.url.includes("/rename")).length, 0);

  await typeName("Peru");
  await submit();
  assert.deepEqual(m.statuses.pop(), [
    "an entry with that name already exists",
    true,
  ]);
  await typeName("Net");
  v.fail.add({ method: "POST", path: "/v1/memory/rename" });
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not rename the entry", true]);
  v.fail.clear();
  v.fail.add({ method: "POST", path: "/v1/memory/rename", status: 500 });
  await typeName("Net");
  await submit();
  assert.deepEqual(m.statuses.pop(), ["could not rename the entry", true]);
  v.fail.clear();

  await typeName("Naples");
  await submit();
  assert.ok(v.files["trips/Naples.md"]);
  assert.equal(v.files["trips/Rome.md"], undefined);
  assert.equal(text(q(m.root, "entry-place")), "trips › Naples");
  assert.equal(q(m.root, "entry-rename-name"), null);
  await m.done();
});

test("renaming with no note open does nothing", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.renameEntry("x"));
  assert.equal(v.calls.length, 0);
  await m.done();
});

test("deleting with no note open does nothing", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.deleteEntry());
  await m.run((n) => n.saveEntry());
  assert.equal(v.calls.length, 0);
  await m.done();
});

test("search shows the passages, then the tree again when cleared; the newest answer wins", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await type(q(m.root, "memory-search"), "Peru");
  assert.deepEqual(
    qa(m.root, "memory-hit").map((b) => text(b).slice(0, 8)),
    ["Japan.md"],
  );
  await click(q(m.root, "memory-hit"));
  assert.match(text(q(m.root, "entry-preview")), /See Peru/);
  await type(q(m.root, "memory-search"), "zzz");
  assert.match(text(m.root), /Nothing matches that/);
  await type(q(m.root, "memory-search"), "  ");
  assert.equal(qa(m.root, "entry-row").length, 2);

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
  await m2.run((x) => x.openMemory());
  await act(() => {
    void m2.hook.current.search("Peru");
    void m2.hook.current.search("Back");
  });
  await settle();
  release();
  await settle();
  assert.deepEqual(
    m2.hook.current.answer.hits.map((x) => x.path),
    ["trips/Peru.md"],
  );
  await m2.done();
  await m.done();
});

test("search failures show no hits", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  v.fail.add({ method: "GET", path: "/search", status: 500 });
  await type(q(m.root, "memory-search"), "Peru");
  assert.equal(qa(m.root, "memory-hit").length, 0);
  v.fail.clear();
  v.fail.add({ method: "GET", path: "/search" });
  await type(q(m.root, "memory-search"), "Peru!");
  assert.equal(qa(m.root, "memory-hit").length, 0);
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
  v.fail.add({ method: "GET", path: "/v1/memory", status: 500 });
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  // An unreadable memory is said, and shown as such, never as an empty one.
  assert.deepEqual(m.statuses.pop(), ["could not load the memory", true]);
  assert.ok(q(m.root, "memory-load-failed"));
  v.fail.clear();
  v.fail.add({ method: "GET", path: "/v1/memory" });
  await m.run((n) => n.openMemory());
  assert.deepEqual(m.statuses.pop(), ["could not load the memory", true]);
  v.fail.clear();
  await m.run((n) => n.openMemory());
  assert.equal(qa(m.root, "memory-load-failed").length, 0);
  await m.run((n) => n.openEntry("missing.md"));
  assert.deepEqual(m.statuses.pop(), ["could not open the entry", true]);
  v.fail.add({ method: "GET", path: "Japan.md" });
  await m.run((n) => n.openEntry("Japan.md"));
  assert.deepEqual(m.statuses.pop(), ["could not open the entry", true]);
  await m.done();
});

test("a link to nothing opens nothing, and a plain close with no note works", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await m.run((n) => n.followLink("https://example.com"));
  await m.run((n) => n.followLink(null));
  assert.equal(q(m.root, "entry-mode"), null);
  // A new link with no note open goes to the top level.
  await m.run((n) => n.followLink("#new:Loose"));
  assert.equal(v.files["Loose.md"], "# Loose\n\n");
  await m.done();
});

test("the preview of a note without links still draws", async () => {
  const m = await mount(vault({ "a.md": "plain" }));
  await m.run((n) => n.openMemory());
  await click(q(m.root, "entry-row"));
  assert.match(text(q(m.root, "entry-preview")), /plain/);
  assert.match(
    text(q(m.root, "backlinks")),
    /No other entry mentions this one yet/,
  );
  await m.done();
});

test("every string of the notes window has its Spanish", async () => {
  const fs = await import("node:fs");
  const files = [
    "MemoryModal.tsx",
    "MemoryLibrary.tsx",
    "MemoryReader.tsx",
    "MemoryMenus.tsx",
    "MemoryGraphPane.tsx",
    "useMemory.ts",
  ];
  for (const k of [
    "1 entry will be deleted.",
    "{n} entries will be deleted.",
    "Deletes its 1 entry",
    "Deletes its {n} entries",
    "Delete 1 entry",
    "Delete {n} entries",
    "1 entry deleted",
    "{n} entries deleted",
  ]) {
    assert.ok(es[k], `missing Spanish for: ${k}`);
  }
  for (const f of files) {
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
  await m.run((n) => n.openMemory());
  await click(qa(m.root, "entry-row")[1]);
  await click(q(m.root, "memory-graph-toggle"));
  assert.ok(q(m.root, "memory-graph"));
  assert.equal(
    q(m.root, "entry-mode"),
    null,
    "the open note is left for the graph",
  );
  assert.match(
    text(q(m.root, "memory-graph")),
    /2 entries · 2 links · 1 to entries not written yet/,
  );
  assert.equal(qa(m.root, "graph-node").length, 2);
  assert.equal(q(m.root, "memory-graph").querySelectorAll("line").length, 2);

  // Back to the list, then the toggle brings the graph again and closes it.
  await click(q(m.root, "graph-back"));
  assert.equal(q(m.root, "memory-graph"), null);
  await click(q(m.root, "memory-graph-toggle"));
  await click(q(m.root, "memory-graph-toggle"));
  assert.equal(q(m.root, "memory-graph"), null);

  // A click on a dot shows its note beside the map; so does Enter, and another key does nothing.
  await click(q(m.root, "memory-graph-toggle"));
  const node = (name) =>
    qa(m.root, "graph-node").find((n) => n.getAttribute("aria-label") === name);
  await act(() =>
    node("Peru").dispatchEvent(
      new W.KeyboardEvent("keydown", { key: "a", bubbles: true }),
    ),
  );
  assert.equal(q(m.root, "graph-peek"), null);
  await act(() =>
    node("Peru").dispatchEvent(
      new W.KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
    ),
  );
  await settle();
  const peek = () => q(m.root, "graph-peek");
  assert.match(text(peek()), /Peru/);
  assert.match(text(peek()), /trips/, "its folders");
  assert.ok(q(m.root, "memory-graph"), "the map stays");
  assert.match(text(q(m.root, "graph-legend")), /trips/);
  assert.match(text(q(m.root, "graph-legend")), /General/);

  // Its links lead to the next note to look at; a link to a note not written yet, or a click
  // beside the links, stays.
  const link = (name) =>
    [...q(m.root, "graph-peek-body").querySelectorAll("a")].find(
      (a) => text(a) === name,
    );
  await click(link("Japan"));
  await settle();
  assert.match(text(q(peek(), "graph-peek-body")), /See Peru and Nowhere/);
  assert.doesNotMatch(
    text(peek()),
    /trips/,
    "a note at the top has no folders",
  );
  await click(link("Nowhere"));
  await click(q(m.root, "graph-peek-body"));
  await settle();
  assert.match(text(q(peek(), "graph-peek-body")), /See Peru/);

  // A note that cannot be read shows nothing; the close button hides the look.
  v.fail.add({ method: "GET", path: "Peru.md" });
  await click(link("Peru"));
  await settle();
  assert.equal(peek(), null);
  v.fail.clear();
  await click(node("Peru"));
  await settle();
  v.fail.add({ method: "GET", path: "Japan.md", status: 404 });
  await click(link("Japan"));
  await settle();
  assert.equal(peek(), null);
  v.fail.clear();
  await click(node("Japan"));
  await settle();
  await click(q(m.root, "graph-peek-close"));
  assert.equal(peek(), null);

  // Open the note leaves the map for it.
  await click(node("Peru"));
  await settle();
  await click(q(m.root, "graph-peek-open"));
  await settle();
  assert.ok(q(m.root, "entry-preview"));
  assert.equal(q(m.root, "memory-graph"), null);

  // Closing the window drops the graph too.
  await click(q(m.root, "memory-graph-toggle"));
  await click(m.root.querySelector('button[aria-label="Close"]'));
  assert.equal(q(m.root, "memory-graph"), null);
  await m.done();
});

test("an empty vault has nothing to draw, and a failing graph is said", async () => {
  const v = vault();
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(q(m.root, "memory-graph-toggle"));
  assert.match(text(q(m.root, "memory-graph")), /Nothing to draw yet/);
  await click(q(m.root, "memory-graph-toggle"));

  v.fail.add({ method: "GET", path: "/graph", status: 500 });
  await click(q(m.root, "memory-graph-toggle"));
  assert.deepEqual(m.statuses.pop(), ["could not load the graph", true]);
  v.fail.clear();
  v.fail.add({ method: "GET", path: "/graph" });
  await click(q(m.root, "memory-graph-toggle"));
  assert.deepEqual(m.statuses.pop(), ["could not load the graph", true]);
  assert.equal(q(m.root, "memory-graph"), null);

  // A draft that cannot be saved keeps the person in the note instead of the graph.
  v.fail.clear();
  const w = vault(FILES);
  const m2 = await mount(w);
  await m2.run((n) => n.openMemory());
  await click(qa(m2.root, "entry-row")[0]);
  await click(q(m2.root, "entry-mode"));
  await type(q(m2.root, "entry-editor"), "changed");
  w.fail.add({ method: "PUT", path: "/v1/memory/entry", status: 500 });
  await click(q(m2.root, "memory-graph-toggle"));
  assert.equal(q(m2.root, "memory-graph"), null);
  assert.ok(q(m2.root, "entry-editor"));
  await m2.done();
  await m.done();
});

test("Review closes the notes and sends the review request in a new conversation", async () => {
  const m = await mount(vault(FILES));
  await m.run((n) => n.openMemory());
  await pick(m.root, "library-menu", "memory-review");
  assert.deepEqual(m.review.sent, [REVIEW_MEMORY_PROMPT]);
  assert.match(REVIEW_MEMORY_PROMPT, /reviewing-the-memory/);
  assert.equal(q(m.root, "memory-search"), null, "the window closed");

  // No conversation could be opened: nothing is sent.
  m.review.opened = undefined;
  await m.run((n) => n.openMemory());
  await pick(m.root, "library-menu", "memory-review");
  assert.equal(m.review.sent.length, 1);
  await m.done();
});

test("Review stays put when the open note cannot be saved", async () => {
  const v = vault(FILES);
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(qa(m.root, "entry-row")[0]);
  await click(q(m.root, "entry-mode"));
  await type(q(m.root, "entry-editor"), "changed");
  v.fail.add({ method: "PUT", path: "", status: 500 });
  await pick(m.root, "library-menu", "memory-review");
  assert.equal(m.review.sent.length, 0);
  assert.ok(q(m.root, "memory-search"), "the window is still open");
  await m.done();
});

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
    edges: [
      { from: "a.md", to: "b.md" },
      { from: "b.md", to: "c.md" },
    ],
    missing: 0,
  };
  const one = api.layoutGraph3D(graph, 100);
  assert.deepEqual(one, api.layoutGraph3D(graph, 100));
  for (const p of one) {
    assert.ok(
      Math.hypot(p.x, p.y, p.z) <= 115.0001,
      "just past the radius at most",
    );
  }
  const ab = Math.hypot(one[0].x - one[1].x, one[0].y - one[1].y);
  const ac = Math.hypot(one[0].x - one[2].x, one[0].y - one[2].y);
  assert.ok(ab > 0, "the first two notes are apart");
  assert.ok(ac > 0, "the first and third notes are apart");
  // A long chain fits most notes in the radius and brings the ends in to just past it.
  const chain = Array.from({ length: 8 }, (_, i) => ({
    path: `n${i}.md`,
    title: "",
    size: 0,
    modified: "",
    in: 0,
    out: 0,
  }));
  const far = api
    .layoutGraph3D(
      {
        nodes: chain,
        edges: chain
          .slice(1)
          .map((n, i) => ({ from: chain[i].path, to: n.path })),
        missing: 0,
      },
      100,
    )
    .map((p) => Math.hypot(p.x, p.y, p.z));
  assert.ok(Math.max(...far) > 100, "the ends stay past the radius");
  assert.ok(Math.max(...far) <= 115.0001, "but close to it");
  assert.deepEqual(
    api.layoutGraph3D({ nodes: [], edges: [], missing: 0 }, 100),
    [],
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

  // A search lights what it finds and turns to it.
  const glowing = fakeHost(300, 200);
  const m3 = map3d.mountGraph3D(T, glowing, GRAPH, () => {}, false);
  const glowLabels = qa(glowing, "graph-node");
  assert.equal(m3.find(" ONE "), 1);
  assert.deepEqual(
    glowLabels.map((b) => b.className),
    ["graph3d-label is-hit", "graph3d-label is-dim"],
  );
  assert.equal(m3.find("o"), 2);
  await new Promise((res) => setTimeout(res, 5));
  assert.equal(m3.find(""), 0);
  assert.deepEqual(
    glowLabels.map((b) => b.className),
    ["graph3d-label", "graph3d-label"],
  );
  m3.stop();
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
  await m.run((n) => n.openMemory());
  await click(q(m.root, "memory-graph-toggle"));
  await settle();
  assert.ok(q(m.root, "graph-3d-hint"), "the 3D map is up");
  assert.equal(q(m.root, "memory-graph").querySelector("svg"), null);
  assert.match(text(q(m.root, "memory-graph-toggle")), /Close map/);
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
  await settle();
  assert.ok(q(m.root, "graph-peek"), "a label shows its note beside the map");
  assert.ok(q(m.root, "graph-legend"));
  await click(q(m.root, "graph-peek-open"));
  await settle();
  assert.ok(q(m.root, "entry-preview"), "and from there it opens");
  assert.equal(q(m.root, "memory-graph"), null);

  // Closed before three.js arrives: nothing is drawn.
  const load = map3d.graph3d.load;
  let release;
  map3d.graph3d.load = () => new Promise((r) => (release = r));
  await click(q(m.root, "memory-graph-toggle"));
  const before = globalThis.__threeRenderer;
  await click(q(m.root, "memory-graph-toggle"));
  release(await import("three"));
  await settle();
  assert.equal(globalThis.__threeRenderer, before);
  map3d.graph3d.load = load;

  // No WebGL context after all: the flat map stays, and its search fades what it does not find.
  globalThis.__threeFail = true;
  await click(q(m.root, "memory-graph-toggle"));
  await settle();
  assert.equal(q(m.root, "graph-3d-hint"), null);
  assert.ok(q(m.root, "memory-graph").querySelector("svg"));
  await find("jap");
  assert.deepEqual(
    qa(m.root, "graph-node").map((g) => g.getAttribute("class")),
    [
      "cursor-pointer graph2d-node is-hit",
      "cursor-pointer graph2d-node is-dim",
    ],
  );
  await click(q(m.root, "memory-graph-toggle"));
  globalThis.__threeFail = false;

  await m.done();

  // An empty vault never asks for three.js.
  const e = await mount(vault());
  await e.run((n) => n.openMemory());
  await click(q(e.root, "memory-graph-toggle"));
  assert.match(text(q(e.root, "memory-graph")), /Nothing to draw yet/);
  await e.done();
  map3d.graph3d.webgl = () => false;
});

// ---- the library: collections, search filters, folders --------------------------------------

const BOOK = {
  "Engineering/Deploys.md": "# Deploys\nSee [[Postmortems]]",
  "Engineering/sub/Deep.md": "# Deep",
  "HR/Payroll.md": "# Payroll",
  "top.md": "# Top",
};
const SPACES = [
  { name: "", entries: 1, modified: "" },
  { name: "Engineering", entries: 2, modified: "" },
  { name: "HR", entries: 1, modified: "" },
];

async function shelf() {
  const v = vault(BOOK);
  v.state.namespaces = SPACES;
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  return { v, m };
}

test("deleting a folder says what goes, asks first, and closes the entry that was in it", async () => {
  const { v, m } = await shelf();
  const names = qa(m.root, "entry-folder").map(
    (f) => f.querySelector("summary span").textContent,
  );
  assert.deepEqual(names.slice(0, 2), ["Engineering", "sub"]);
  // Engineering holds two entries.
  await pick(m.root, "folder-menu", "folder-delete", 0);
  assert.match(text(qd("entry-confirm")), /Delete this folder of entries\?/);
  assert.match(text(qd("entry-confirm-count")), /2 entries will be deleted\./);
  assert.match(text(qd("entry-confirm-yes")), /Delete 2 entries/);
  await click(qd("entry-confirm-no"));
  assert.equal(qd("entry-confirm"), null);
  assert.equal(
    v.calls.some((c) => c.method === "DELETE"),
    false,
  );

  // HR holds one, and the open entry in it closes.
  await m.run((n) => n.openEntry("HR/Payroll.md"));
  await pick(m.root, "folder-menu", "folder-delete", 2);
  assert.match(text(qd("entry-confirm-count")), /1 entry will be deleted\./);
  assert.match(text(qd("entry-confirm-yes")), /Delete 1 entry$/);
  await click(qd("entry-confirm-yes"));
  assert.equal(v.files["HR/Payroll.md"], undefined);
  assert.deepEqual(m.statuses.pop(), ["1 entry deleted", false]);
  assert.equal(q(m.root, "entry-mode"), null, "the open entry was in it");

  // An entry outside the folder stays open.
  await m.run((n) => n.openEntry("top.md"));
  await pick(m.root, "folder-menu", "folder-delete", 0);
  await click(qd("entry-confirm-yes"));
  assert.deepEqual(m.statuses.pop(), ["2 entries deleted", false]);
  assert.ok(q(m.root, "entry-mode"), "top.md is still open");
  await m.done();
});

test("a folder that cannot be deleted is said, and the entries stay", async () => {
  const { v, m } = await shelf();
  v.fail.add({ method: "DELETE", path: "/v1/memory/folder", status: 500 });
  await pick(m.root, "folder-menu", "folder-delete", 2);
  await click(qd("entry-confirm-yes"));
  assert.deepEqual(m.statuses.pop(), ["could not delete the folder", true]);
  v.fail.clear();
  v.fail.add({ method: "DELETE", path: "/v1/memory/folder" });
  await pick(m.root, "folder-menu", "folder-delete", 2);
  await click(qd("entry-confirm-yes"));
  assert.deepEqual(m.statuses.pop(), ["could not delete the folder", true]);
  assert.ok(v.files["HR/Payroll.md"]);
  await m.done();
});

test("searchPath sends only the filters that are set", () => {
  const none = api.searchPath("pay roll", api.NO_FILTERS);
  assert.equal(none, "/v1/memory/search?q=pay+roll");
  const all = api.searchPath("x", {
    namespace: "HR",
    author: "Ana",
    since: "2026-01-01",
    until: "2026-02-01",
    graph: true,
  });
  assert.equal(
    all,
    "/v1/memory/search?q=x&namespace=HR&author=Ana&since=2026-01-01&until=2026-02-01&graph=1",
  );
});

test("the filters rerun the search, and the answer shows related entries and bylines", async () => {
  const v = vault(BOOK);
  v.state.namespaces = SPACES;
  const answer = {
    hits: [
      {
        path: "HR/Payroll.md",
        title: "Payroll",
        namespace: "HR",
        heading: "Pay days",
        line: 3,
        snippet: "paid on the [[last]] day",
        block: "x",
        author: "Ana",
        date: "2026-03-10T00:00:00Z",
        score: 1,
      },
      {
        path: "top.md",
        title: "Top",
        namespace: "",
        line: 1,
        snippet: "",
        block: "x",

        score: 0.5,
      },
    ],
    related: [
      {
        path: "Engineering/Deploys.md",
        title: "Deploys",
        namespace: "Engineering",
        line: 1,
        snippet: "s",
        block: "x",
        date: "",
        score: 0.1,
        via: "Payroll",
      },
    ],
  };
  const base = v.gw;
  const searches = [];
  v.gw = async (url, init) => {
    if (url.startsWith("/v1/memory/search")) {
      searches.push(url);
      return json(answer);
    }
    return base(url, init);
  };
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  const select = q(m.root, "filter-namespace");
  assert.deepEqual(
    [...select.options].map((o) => o.textContent),
    ["All collections", "General (1)", "Engineering (2)", "HR (1)"],
  );
  await type(q(m.root, "memory-search"), "pay");
  const hits = qa(m.root, "memory-hit").map((b) => text(b));
  assert.match(hits[0], /Payroll/);
  assert.match(hits[0], /HR › Pay days/);
  assert.match(hits[0], /Ana · 2026-03-10/);
  assert.doesNotMatch(hits[1], /·/);
  assert.match(hits[2], /via Payroll/);
  assert.ok(q(m.root, "memory-related"));

  select.value = "HR";
  await act(() =>
    select.dispatchEvent(new W.Event("change", { bubbles: true })),
  );
  await settle();
  const box = q(m.root, "filter-graph");
  box.checked = true;
  await act(() => box.dispatchEvent(new W.Event("change", { bubbles: true })));
  await settle();
  await type(q(m.root, "filter-author"), "Ana");
  await type(q(m.root, "filter-since"), "2026-01-01");
  await type(q(m.root, "filter-until"), "2026-12-01");
  assert.equal(
    searches.pop(),
    "/v1/memory/search?q=pay&namespace=HR&author=Ana&since=2026-01-01&until=2026-12-01&graph=1",
  );

  // Opening a hit opens its entry; no hits says so; closing clears the filters.
  await click(qa(m.root, "memory-hit")[0]);
  assert.ok(q(m.root, "entry-mode"));
  answer.hits = [];
  answer.related = undefined;
  await type(q(m.root, "memory-search"), "none");
  assert.match(text(m.root), /Nothing matches that/);
  await m.done();
});

test("a new entry offers the collections that exist, and shows who wrote an entry", async () => {
  const v = vault({
    ...BOOK,
    "Engineering/Signed.md": "---\nauthor: Ana\ndate: 2026-03-10\n---\n# S",
  });
  v.state.namespaces = SPACES;
  const base = v.gw;
  v.gw = async (url, init) => {
    const res = await base(url, init);
    if (url.includes("Signed.md") && !init) {
      const view = await res.json();
      return json({
        ...view,
        meta: { author: "Ana", date: "2026-03-10T00:00:00Z" },
      });
    }
    if (url.includes("Payroll.md") && !init) {
      const view = await res.json();
      return json({ ...view, meta: { date: "" } });
    }
    return res;
  };
  const m = await mount(v);
  await m.run((n) => n.openMemory());
  await click(q(m.root, "entry-new-toggle"));
  const options = [...m.root.querySelectorAll("#memory-collections option")];
  assert.deepEqual(
    options.map((o) => o.value),
    ["Engineering", "HR"],
  );
  await type(q(m.root, "entry-new-name"), "Roadmap");
  await type(q(m.root, "entry-new-folder"), "Engineering");
  await click(q(m.root, "entry-create"));
  assert.ok(v.files["Engineering/Roadmap.md"]);
  await m.run((n) => n.openEntry("Engineering/Signed.md"));
  assert.equal(text(q(m.root, "entry-by")), "Ana · 2026-03-10");
  await m.run((n) => n.openEntry("HR/Payroll.md"));
  assert.equal(q(m.root, "entry-by"), null);
  await m.done();
});

test("plainWiki shows the words of a line: links by their alias or name, marks gone", () => {
  assert.equal(
    api.plainWiki("> - **See** [[trips/Japan]] and [[Peru|the trip]] `now`"),
    "See Japan and the trip now",
  );
  assert.equal(api.plainWiki("an open [[link"), "an open [[link");
});
