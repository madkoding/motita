// The kinds of memory, the associations between entries and the reorganization of the memory: the
// pure helpers (memoryKinds.ts) and, over a fake gateway, the window that shows them (MemoryFacts,
// MemoryReorganize, useReorganize and what the reader, the library and the map draw of them).
// Run with: node --test web/src/__tests__/cov-memorykinds.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text } from "./cov-render-helpers.mjs";

const dom = setupDom();
const W = dom.window;
globalThis.requestAnimationFrame = (cb) =>
  setTimeout(() => cb(performance.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const kinds = await import("../memoryKinds.ts");
const api = await import("../memoryApi.ts");
const map3d = await import("../memoryGraph3d.ts");
map3d.graph3d.webgl = () => false;
const { useMemory } = await load("useMemory.ts");
const { REORG_POLL } = await load("useReorganize.ts");
const { MemoryModal } = await load("MemoryModal.tsx");
const { factsLine } = await load("MemoryFacts.tsx");
const { AppCtx } = await load("appStore.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

REORG_POLL.ms = 1;

const settle = async (n = 10) => {
  for (let i = 0; i < n; i++) {
    await act(() => new Promise((r) => setTimeout(r, 2)));
  }
};
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// ---- the pure helpers ------------------------------------------------------------------------

test("kinds, relations and steps are named in plain words", () => {
  assert.equal(kinds.kindLabel("decision"), "Decisions");
  assert.equal(kinds.kindLabel(undefined), "Not sorted yet");
  assert.equal(kinds.kindLabel("odd"), "Not sorted yet");
  assert.equal(kinds.relationLabel("replaces"), "Replaces");
  assert.equal(kinds.relationLabel("odd"), "Related to");
  assert.equal(kinds.stepLabel("classify"), "Sorting each entry by kind…");
  assert.equal(kinds.stepLabel("odd"), "Working…");
  assert.equal(kinds.authorName(undefined), "");
  assert.equal(kinds.authorName("motita"), "Motita");
  assert.equal(kinds.authorName("Ana"), "Ana");
});

test("stripFront hides the front matter and keeps the text", () => {
  assert.equal(kinds.stripFront("---\ntype: fact\n---\n\nHi"), "\nHi");
  assert.equal(kinds.stripFront("---\r\na: b\r\n---"), "");
  assert.equal(kinds.stripFront("Hi\n---\nx"), "Hi\n---\nx");
});

test("relationLine reads an association stated in front matter", () => {
  assert.deepEqual(kinds.relationLine(' Part-Of: "[[A]]"'), {
    kind: "part_of",
    rest: '"[[A]]"',
  });
  assert.equal(kinds.relationLine("See [[A]]"), null);
});

test("groupRelations groups by kind and finds where each leads", () => {
  const groups = kinds.groupRelations(
    [
      { kind: "replaces", target: "Old" },
      { kind: "related", target: "A" },
      { kind: "replaces", target: "Gone" },
    ],
    [{ target: "old", path: "x/Old.md" }, { target: "A" }],
  );
  assert.deepEqual(groups, [
    {
      kind: "replaces",
      items: [
        { target: "Old", path: "x/Old.md" },
        { target: "Gone", path: undefined },
      ],
    },
    { kind: "related", items: [{ target: "A", path: undefined }] },
  ]);
  assert.deepEqual(kinds.groupRelations(undefined, []), []);
});

test("reportLines tells only what happened", () => {
  const r = {
    backup: "b",
    entries: 9,
    typed: 3,
    renamed: 0,
    merged: 1,
    replaced: 0,
    contradictions: 2,
    linked: 0,
    joined: 0,
    unanswered: 0,
    used_model: true,
  };
  assert.deepEqual(kinds.reportLines(r), [
    { key: "{n} entries sorted by kind", n: 3 },
    { key: "1 duplicate merged", n: 1 },
    { key: "{n} contradictions marked", n: 2 },
  ]);
});

test("the map colours by kind once any entry has one", () => {
  const plain = [
    { path: "a/x.md", title: "x", in: 0, out: 0 },
    { path: "y.md", title: "y", in: 0, out: 0 },
  ];
  assert.equal(api.mapGroups(plain)[0].byKind, false);
  const typed = [
    { path: "a/x.md", title: "x", in: 0, out: 0, type: "fact" },
    { path: "b/z.md", title: "z", in: 0, out: 0, type: "fact" },
    { path: "y.md", title: "y", in: 0, out: 0 },
  ];
  const groups = api.mapGroups(typed);
  assert.deepEqual(
    groups.map((g) => [g.key, g.count, g.byKind]),
    [
      ["fact", 2, true],
      ["", 1, true],
    ],
  );
  const colours = api.entryColours(typed, 8);
  assert.equal(colours.get("a/x.md"), colours.get("b/z.md"));
  assert.notEqual(colours.get("a/x.md"), colours.get("y.md"));
  assert.equal(
    api.searchPath("q", { ...api.NO_FILTERS, type: "decision" }),
    "/v1/memory/search?q=q&type=decision",
  );
});

test("factsLine says what an entry is in one line", () => {
  assert.equal(
    factsLine({
      type: "decision",
      author: "motita",
      date: "2026-11-03T10:00:00Z",
      project: "Shop",
      repos: ["acme/shop", "acme/api"],
    }),
    "Decisions · Motita · 2026-11-03 · Shop · acme/shop, acme/api",
  );
  assert.equal(factsLine(undefined), "");
});

// ---- the window, over a fake gateway -------------------------------------------------------------

const ENTRIES = {
  "Shop/Deploys.md": {
    body: '---\ntype: decision\nreplaces: "[[Old]]"\nrelated: "[[Nowhere]]"\n---\n\nWe deploy on Fridays.',
    meta: {
      author: "motita",
      date: "2026-11-03",
      type: "decision",
      project: "Shop",
      repos: ["acme/shop"],
      relations: [
        { kind: "replaces", target: "Old" },
        { kind: "related", target: "Nowhere" },
      ],
    },
    links: [{ target: "Old", path: "Old.md" }, { target: "Nowhere" }],
  },
  "Old.md": {
    body: "We deployed on Mondays.",
    meta: { date: "2026-01-01" },
    links: [],
  },
};

// gateway answers the memory's calls; reorg is the reorganization's script: the statuses GET answers
// in turn, the last one repeated.
function gateway({
  reorg = [],
  startStatus = 202,
  undoStatus = 200,
  failRead = false,
} = {}) {
  const calls = [];
  let reads = 0;
  const gw = async (url, init = {}) => {
    const method = init.method || "GET";
    calls.push(method + " " + url);
    const u = new URL(url, "http://x");
    if (u.pathname === "/v1/memory") {
      return json({
        root: "/m",
        entries: Object.keys(ENTRIES).map((p) => ({
          path: p,
          title: p,
          size: 1,
          modified: "",
        })),
        namespaces: [],
      });
    }
    if (u.pathname === "/v1/memory/entry") {
      const p = u.searchParams.get("path");
      const e = ENTRIES[p];
      return json({
        path: p,
        title: p.split("/").pop().replace(/\.md$/, ""),
        size: 1,
        modified: "",
        body: e.body,
        meta: e.meta,
        links: e.links,
        backlinks:
          p === "Old.md"
            ? [
                {
                  path: "Shop/Deploys.md",
                  title: "Deploys",
                  size: 1,
                  modified: "",
                  context: 'replaces: "[[Old]]"',
                },
                {
                  path: "x.md",
                  title: "x",
                  size: 1,
                  modified: "",
                  context: "See [[Old]]",
                },
              ]
            : [],
      });
    }
    if (u.pathname === "/v1/memory/graph") {
      return json({
        nodes: [
          {
            path: "Shop/Deploys.md",
            title: "Deploys",
            in: 0,
            out: 1,
            type: "decision",
          },
          { path: "Old.md", title: "Old", in: 1, out: 0 },
        ],
        edges: [{ from: "Shop/Deploys.md", to: "Old.md", kind: "contradicts" }],
        missing: 0,
      });
    }
    if (u.pathname === "/v1/memory/search") {
      return json({
        hits: [
          {
            path: "Shop/Deploys.md",
            title: "Deploys",
            namespace: "Shop",
            line: 1,
            snippet: "We deploy",
            block: "We deploy",
            author: "motita",
            type: "decision",
            date: "2026-11-03",
            score: 1,
          },
        ],
      });
    }
    if (u.pathname === "/v1/memory/reorganize/undo") {
      return json({ restored: true }, undoStatus);
    }
    if (u.pathname === "/v1/memory/reorganize") {
      if (method === "POST") {
        return json({}, startStatus);
      }
      if (method === "DELETE") {
        return json({});
      }
      if (failRead) {
        throw new Error("network");
      }
      if (reorg.length === 0) {
        return json({}, 500);
      }
      const st = reorg[Math.min(reads, reorg.length - 1)];
      reads++;
      return json(st);
    }
    return json({ error: "?" }, 404);
  };
  return { gw, calls };
}

async function mount(g) {
  const hook = { current: null };
  function Harness() {
    const memory = useMemory(g.gw, () => {});
    hook.current = memory;
    return h(
      AppCtx.Provider,
      {
        value: {
          memory,
          reviewMemory: async () => {},
          lists: { projects: [] },
        },
      },
      memory.showMemory ? h(MemoryModal, {}) : null,
    );
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(Harness, {}), root));
  return {
    root,
    hook,
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
const q = (root, id) => root.querySelector(`[data-testid="${id}"]`);
const qa = (root, id) => [...root.querySelectorAll(`[data-testid="${id}"]`)];
const qd = (id) => document.querySelector(`[data-testid="${id}"]`);

test("an entry shows what it is in words and its associations, never its front matter", async () => {
  const m = await mount(gateway());
  await m.run((n) => n.openMemory());
  await m.run((n) => n.openEntry("Shop/Deploys.md"));
  const by = text(q(m.root, "entry-by"));
  assert.equal(by, "Decisions · Motita · 2026-11-03 · Shop · acme/shop");
  assert.doesNotMatch(text(m.root), /type: decision/);
  const assoc = q(m.root, "entry-associations");
  assert.match(text(assoc), /Associated with/);
  assert.match(text(assoc), /Replaces\s*Old/);
  assert.match(text(assoc), /Related to\s*Nowhere/);
  assert.equal(qa(m.root, "association").length, 1, "only what exists opens");
  await click(q(m.root, "association"));
  assert.equal(m.hook.current.current.path, "Old.md");
  assert.match(text(m.root), /Reemplaza a Old|Replaces Old/);
  assert.match(text(m.root), /See Old/);
  assert.equal(
    q(m.root, "entry-associations"),
    null,
    "no associations, no section",
  );
  await m.done();
});

test("a search narrows by kind and says the kind of each answer", async () => {
  const g = gateway();
  const m = await mount(g);
  await m.run((n) => n.openMemory());
  await m.run((n) => n.search("deploy"));
  const sel = q(m.root, "filter-type");
  assert.equal(sel.options.length, 8);
  sel.value = "decision";
  await act(() => sel.dispatchEvent(new W.Event("change", { bubbles: true })));
  await settle();
  assert.ok(
    g.calls.some((c) => c.includes("type=decision")),
    g.calls.join("\n"),
  );
  assert.match(
    text(q(m.root, "memory-hit")),
    /Decisions · Motita · 2026-11-03/,
  );
  await m.done();
});

test("the map names kinds, relations and shows an entry's facts beside it", async () => {
  const m = await mount(gateway());
  await m.run((n) => n.openMemory());
  await m.run((n) => n.openGraph());
  const legend = text(q(m.root, "graph-legend"));
  assert.match(legend, /Decisions/);
  assert.match(legend, /Not sorted yet/);
  const line = m.root.querySelector("svg[role=img] line");
  assert.equal(line.getAttribute("stroke-dasharray"), "4 3");
  assert.equal(text(line), "Contradicts");
  await click(qa(m.root, "graph-node")[0]);
  assert.match(text(q(m.root, "graph-peek-facts")), /Decisions · Motita/);
  assert.doesNotMatch(text(q(m.root, "graph-peek-body")), /type: decision/);
  await click(q(m.root, "association"));
  assert.match(text(q(m.root, "graph-peek")), /Old/);
  await m.done();
});

const REPORT = {
  backup: "20261103-180000",
  entries: 12,
  typed: 4,
  renamed: 0,
  merged: 1,
  replaced: 0,
  contradictions: 0,
  linked: 2,
  joined: 0,
  unanswered: 2,
  used_model: true,
};

const openReorg = async (m) => {
  await click(q(m.root, "library-menu"));
  await click(qd("memory-reorganize"));
};

test("the memory is reorganized from its menu, followed while it runs, and undone", async () => {
  const g = gateway({
    reorg: [
      { running: false, progress: { step: "", done: 0, total: 0 } },
      { running: true, progress: { step: "classify", done: 3, total: 12 } },
      { running: true, progress: { step: "backup", done: 0, total: 0 } },
      {
        running: false,
        progress: { step: "associate", done: 5, total: 5 },
        report: REPORT,
      },
    ],
  });
  const m = await mount(g);
  await m.run((n) => n.openMemory());
  await openReorg(m);
  assert.match(text(qd("reorg-dialog")), /A copy of your memory is kept first/);
  await click(qd("reorg-start"));
  for (let i = 0; i < 30 && !qd("reorg-report"); i++) {
    await settle();
  }
  assert.match(text(qd("reorg-report")), /4 entries sorted by kind/);
  assert.match(text(qd("reorg-dialog")), /did not answer 2 times/);
  assert.match(text(qd("reorg-dialog")), /12 entries in your memory now/);
  assert.ok(g.calls.includes("POST /v1/memory/reorganize"));
  await click(qd("reorg-undo"));
  assert.ok(g.calls.some((c) => c === "POST /v1/memory/reorganize/undo"));
  assert.equal(qd("reorg-dialog"), null, "an undo closes the window");
  await m.done();
});

test("a running reorganization shows its step and stops", async () => {
  const g = gateway({
    reorg: [
      { running: true, progress: { step: "classify", done: 3, total: 12 } },
      { running: true, progress: { step: "classify", done: 3, total: 12 } },
    ],
  });
  const m = await mount(g);
  await m.run((n) => n.openMemory());
  await openReorg(m);
  assert.match(text(qd("reorg-step")), /Sorting each entry by kind/);
  assert.match(text(qd("reorg-dialog")), /3 of 12/);
  await click(qd("reorg-stop"));
  assert.ok(g.calls.includes("DELETE /v1/memory/reorganize"));
  await m.run((n) => n.reorg.hide());
  assert.equal(qd("reorg-dialog"), null);
  await m.done();
});

test("a reorganization that stopped early, without a model, or with nothing to do says so", async () => {
  const stopped = {
    running: false,
    progress: { step: "", done: 0, total: 0 },
    error: "context canceled",
    report: {
      ...REPORT,
      typed: 0,
      merged: 0,
      linked: 0,
      unanswered: 1,
      used_model: false,
    },
  };
  const m = await mount(gateway({ reorg: [stopped] }));
  await m.run((n) => n.openMemory());
  await openReorg(m);
  const d = text(qd("reorg-dialog"));
  assert.match(d, /It stopped before the end/);
  assert.match(d, /Everything was already in order/);
  assert.match(d, /No model was available/);
  assert.match(d, /did not answer 1 time/);
  await click(qd("reorg-close"));
  assert.equal(qd("reorg-dialog"), null);
  await m.done();
});

test("a reorganization that cannot start, be read or be undone says so", async () => {
  const refused = await mount(gateway({ startStatus: 500 }));
  await refused.run((n) => n.openMemory());
  await openReorg(refused);
  await click(qd("reorg-start"));
  assert.match(text(qd("reorg-failed")), /could not start the reorganization/);
  await click(qd("reorg-cancel"));
  await refused.done();

  const busy = await mount(gateway({ startStatus: 409 }));
  await busy.run((n) => n.openMemory());
  await openReorg(busy);
  await click(qd("reorg-start"));
  assert.equal(
    qd("reorg-failed"),
    null,
    "one already running is followed, not an error",
  );
  await busy.done();

  const offline = await mount(gateway({ failRead: true }));
  await offline.run((n) => n.openMemory());
  await openReorg(offline);
  assert.match(
    text(qd("reorg-failed")),
    /could not read how the reorganization goes/,
  );
  await offline.run((n) => n.reorg.start());
  await offline.done();

  const throwing = gateway();
  const net = await mount({
    gw: async (url, init = {}) => {
      if (url.startsWith("/v1/memory/reorganize")) {
        throw new Error("network");
      }
      return throwing.gw(url, init);
    },
  });
  await net.run((n) => n.openMemory());
  await net.run((n) => n.reorg.show());
  await net.run((n) => n.reorg.start());
  assert.match(text(qd("reorg-failed")), /could not start the reorganization/);
  await net.run((n) => n.reorg.undo());
  assert.match(text(qd("reorg-failed")), /could not undo the reorganization/);
  await net.run((n) => n.reorg.stop());
  await net.done();

  const noUndo = await mount(
    gateway({
      undoStatus: 404,
      reorg: [
        {
          running: false,
          progress: { step: "", done: 0, total: 0 },
          report: REPORT,
        },
      ],
    }),
  );
  await noUndo.run((n) => n.openMemory());
  await openReorg(noUndo);
  await click(qd("reorg-undo"));
  assert.match(text(qd("reorg-failed")), /could not undo the reorganization/);
  await noUndo.done();
});
