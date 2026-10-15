// The artifacts of a session (useArtifacts.ts) and the modal that lists and shows them
// (ArtifactsModal.tsx), run together in jsdom against a fake gateway.
// Run with: node --test web/src/__tests__/cov-render-artifacts.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, text } from "./cov-render-helpers.mjs";

const dom = setupDom();
const W = dom.window;
globalThis.requestAnimationFrame = (cb) => setTimeout(() => cb(performance.now()), 0);
globalThis.cancelAnimationFrame = (id) => clearTimeout(id);

const { useArtifacts } = await load("useArtifacts.ts");
const { ArtifactsModal } = await load("ArtifactsModal.tsx");
const { AppCtx } = await load("appStore.ts");
const { cancelQuiet } = await load("hydration.ts");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

// settle lets the fake gateway's responses (and the reads of their bodies) arrive.
const settle = async (n = 8) => {
  for (let i = 0; i < n; i++) {
    await act(() => new Promise((r) => setImmediate(r)));
  }
};

const DAY = 24 * 3600 * 1000;
const inDays = (d) => new Date(Date.now() + d * DAY - 60_000).toISOString();

// gateway is a fake api: `routes` answers each request from its method and path, and every call
// is kept. A route may return a Response, or throw to stand for a network failure.
function gateway(routes) {
  const calls = [];
  const api = async (url, init = {}) => {
    const method = init.method || "GET";
    calls.push({ method, url, init });
    const r = routes(method, url, init);
    if (r instanceof Error) {
      throw r;
    }
    return r;
  };
  return { api, calls };
}
const json = (body, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const LIST = [
  { name: "report.md", size: 3000, type: "text/markdown", modified: "" },
  { name: "shot.png", size: 100, type: "image/png", modified: "", pinned: true, expires_at: inDays(2) },
  { name: "old.log", size: 10 * 1024, type: "text/plain", modified: "", expires_at: inDays(3) },
  { name: "gone.txt", size: 2048, type: "text/plain", modified: "", expires_at: inDays(-2) },
  { name: "later.txt", size: 2048, type: "text/plain", modified: "", expires_at: inDays(30) },
];

// mount draws the modal (while it is shown) over the real hook.
async function mount({ api, sessionId = "s 1", session = { project_id: "p1" } }) {
  const statuses = [];
  const hook = { current: null };
  function Harness() {
    const artifacts = useArtifacts(api, sessionId, (t, bad) => statuses.push([t, !!bad]));
    hook.current = artifacts;
    return h(
      AppCtx.Provider,
      { value: { artifacts, selectedSession: session } },
      artifacts.showArtifacts ? h(ArtifactsModal, {}) : null,
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
      cancelQuiet();
    },
  };
}

const click = (el) =>
  act(() => {
    el.dispatchEvent(new W.MouseEvent("click", { bubbles: true, cancelable: true }));
  });
const rows = (root) => [...root.querySelectorAll("li")];
const byText = (root, s) => [...root.querySelectorAll("button")].find((b) => text(b) === s);

test("the modal lists the session's files with their size, pin and expiry", async () => {
  const g = gateway(() => json({ artifacts: LIST }));
  const m = await mount(g);
  await m.run((a) => a.openArtifacts());
  assert.deepEqual(g.calls.map((c) => c.url), ["/v1/sessions/s%201/artifacts"]);
  assert.equal(text(m.root.querySelector("h2")), "Artifacts");
  const items = rows(m.root);
  assert.equal(items.length, 5);
  assert.equal(text(items[0].querySelector(".font-mono")), "report.md");
  assert.equal(text(items[0].querySelector(".text-xs")), "text/markdown · 3 KB");
  assert.equal(text(items[1].querySelector(".text-xs")), "image/png · 1 KB", "never less than 1 KB");
  assert.match(text(items[1]), /Pinned: it will not be deleted/);
  assert.equal(items[1].querySelector("[data-testid=artifact-expiry]"), null, "a pinned file does not expire");
  assert.equal(text(items[2].querySelector("[data-testid=artifact-expiry]")), "Deleted in 3 days unless pinned");
  assert.equal(text(items[3].querySelector("[data-testid=artifact-expiry]")), "Deleted in 0 days unless pinned");
  assert.equal(items[4].querySelector("[data-testid=artifact-expiry]"), null, "far from expiring");
  assert.equal(text(items[0].querySelectorAll("button")[1]), "Pin");
  assert.equal(text(items[1].querySelectorAll("button")[1]), "Unpin");
  assert.equal(items[1].querySelectorAll("button")[1].getAttribute("aria-pressed"), "true");
  await m.done();
});

test("a session with a project switches between its files and the project's", async () => {
  const g = gateway((_, url) => json({ artifacts: url.includes("scope=project") ? [LIST[0]] : [] }));
  const m = await mount(g);
  await m.run((a) => a.openArtifacts());
  assert.match(text(m.root), /Nothing saved in this session yet/);
  const tabs = [...m.root.querySelectorAll("[role=tab]")];
  assert.deepEqual(tabs.map(text), ["This session", "Whole project"]);
  assert.equal(tabs[0].getAttribute("aria-selected"), "true");
  await click(tabs[1]);
  await settle();
  assert.equal(g.calls.at(-1).url, "/v1/sessions/s%201/artifacts?scope=project");
  assert.equal(m.root.querySelector("[role=tab][aria-selected=true]").textContent, "Whole project");
  assert.equal(rows(m.root).length, 1);
  // A change in the project scope stays in it.
  await click(rows(m.root)[0].querySelectorAll("button")[1]);
  await settle();
  const pin = g.calls.find((c) => c.method === "POST");
  assert.equal(pin.url, "/v1/sessions/s%201/artifacts/report.md/pin?scope=project");
  assert.deepEqual(JSON.parse(pin.init.body), { pinned: true });
  assert.equal(pin.init.headers["Content-Type"], "application/json");
  await m.done();
});

test("a session without a project has no scope tabs", async () => {
  const g = gateway(() => json({ artifacts: [] }));
  const m = await mount({ ...g, session: { project_id: "" } });
  await m.run((a) => a.openArtifacts());
  assert.equal(m.root.querySelector("[role=tablist]"), null);
  await m.done();
  const none = await mount({ ...g, session: null });
  await none.run((a) => a.openArtifacts());
  assert.equal(none.root.querySelector("[role=tablist]"), null);
  await none.done();
});

test("an unreadable or failed list shows as empty, and no session asks for nothing", async () => {
  for (const reply of [json({ artifacts: "nope" }), json({}, 500), new Error("offline")]) {
    const g = gateway(() => reply);
    const m = await mount(g);
    await m.run((a) => a.openArtifacts());
    assert.deepEqual(m.hook.current.artifacts, []);
    assert.match(text(m.root), /Nothing saved/);
    await m.done();
  }
  const g = gateway(() => json({ artifacts: LIST }));
  const m = await mount({ ...g, sessionId: "" });
  await m.run((a) => a.openArtifacts());
  assert.equal(g.calls.length, 0);
  assert.deepEqual(m.hook.current.artifacts, []);
  await m.done();
});

test("each kind of file opens its own way, and Back returns to the list", async () => {
  const bodies = {
    "report.md": () => new Response("# Heading\n\ntext", { headers: { "Content-Type": "text/markdown" } }),
    "page.html": () => new Response("<p>page</p>"),
    "empty.md": () => new Response(""),
    "data.json": () => new Response('{"a":1}'),
    "notes.txt": () => new Response("plain <notes>"),
    "shot.png": () => new Response(new Blob([new Uint8Array([1, 2])], { type: "image/png" })),
    "doc.pdf": () => new Response(new Blob(["%PDF"], { type: "application/pdf" })),
  };
  const g = gateway((_, url) => {
    const name = decodeURIComponent(url.split("/").pop());
    return bodies[name]?.() ?? json({ artifacts: [] });
  });
  const m = await mount(g);
  await m.run((a) => a.openArtifacts());
  const open = async (name, type) => {
    await m.run((a) => a.openArtifact({ name, type }));
    return m.root;
  };

  let root = await open("report.md", "text/markdown");
  assert.equal(root.querySelector(".markdown-body h1").textContent, "Heading");
  assert.equal(text(root.querySelector(".font-mono.truncate")), "report.md");
  assert.equal(root.querySelector("li"), null, "the list is hidden behind the file");
  await click(byText(root, "Back to the list"));
  assert.equal(m.hook.current.artifactView, null);
  assert.match(text(root), /Nothing saved/);

  root = await open("empty.md", "text/markdown");
  assert.equal(text(root.querySelector(".markdown-body")), "copy", "an empty file is an empty page");

  root = await open("page.html", "text/html");
  const frame = root.querySelector("iframe");
  assert.equal(frame.getAttribute("sandbox"), "");
  assert.equal(frame.getAttribute("srcdoc"), "<p>page</p>");
  assert.equal(frame.title, "page.html");

  root = await open("data.json", "application/json");
  assert.equal(root.querySelector("pre").textContent, '{"a":1}');
  root = await open("notes.txt", "text/plain");
  assert.equal(root.querySelector("pre").textContent, "plain <notes>");

  root = await open("shot.png", "image/png");
  const img = root.querySelector("img");
  assert.match(img.getAttribute("src"), /^blob:/);
  assert.equal(img.alt, "shot.png");

  root = await open("doc.pdf", "application/pdf");
  const pdf = root.querySelector("iframe");
  assert.match(pdf.getAttribute("src"), /^blob:/);
  assert.equal(pdf.hasAttribute("sandbox"), false);
  assert.deepEqual(m.statuses, []);
  await m.done();
});

test("a file the page cannot show is saved instead, and its link revoked after a while", async () => {
  const clicked = [];
  const revoked = [];
  const savedClick = W.HTMLAnchorElement.prototype.click;
  W.HTMLAnchorElement.prototype.click = function () {
    clicked.push([this.download, this.href]);
  };
  const savedRevoke = URL.revokeObjectURL;
  URL.revokeObjectURL = (u) => revoked.push(u);
  const g = gateway(() => new Response(new Blob(["zip"], { type: "application/zip" })));
  const m = await mount(g);
  await m.run((a) => a.openArtifact({ name: "bundle.zip", type: "application/zip" }));
  assert.equal(clicked.length, 1);
  assert.equal(clicked[0][0], "bundle.zip");
  assert.match(clicked[0][1], /^blob:/);
  assert.equal(m.hook.current.artifactView, null);
  await new Promise((r) => setTimeout(r, 1050));
  assert.deepEqual(revoked, [clicked[0][1]]);
  W.HTMLAnchorElement.prototype.click = savedClick;
  URL.revokeObjectURL = savedRevoke;
  await m.done();
});

test("a file that cannot be opened says so", async () => {
  for (const reply of [json({}, 404), new Error("offline")]) {
    const g = gateway(() => reply);
    const m = await mount(g);
    await m.run((a) => a.openArtifact({ name: "x.txt", type: "text/plain" }, "project"));
    assert.equal(g.calls[0].url, "/v1/sessions/s%201/artifacts/x.txt?scope=project");
    assert.deepEqual(m.statuses, [["could not open the artifact", true]]);
    await m.done();
  }
});

test("deleting a file reloads the list and closes it if it was open", async () => {
  let list = [LIST[0], LIST[2]];
  const g = gateway((method, url) => {
    if (method === "DELETE") {
      list = list.filter((a) => !url.includes(encodeURIComponent(a.name)));
      return new Response(null, { status: 204 });
    }
    if (url.endsWith("/artifacts")) {
      return json({ artifacts: list });
    }
    return new Response("text");
  });
  const m = await mount(g);
  await m.run((a) => a.openArtifacts());
  await click(rows(m.root)[1].querySelectorAll("button")[2]);
  await settle();
  assert.equal(g.calls.find((c) => c.method === "DELETE").url, "/v1/sessions/s%201/artifacts/old.log");
  assert.deepEqual(rows(m.root).map((li) => li.querySelector(".font-mono").textContent), ["report.md"]);

  // The file being looked at is closed when it is the one deleted, and kept when another is.
  await m.run((a) => a.openArtifact({ name: "report.md", type: "text/plain" }));
  await m.run((a) => a.deleteArtifact("other.txt"));
  assert.equal(m.hook.current.artifactView.name, "report.md");
  await m.run((a) => a.deleteArtifact("report.md"));
  assert.equal(m.hook.current.artifactView, null);
  assert.deepEqual(m.statuses, []);
  await m.done();
});

test("a refused or failed delete or pin says so and changes nothing", async () => {
  for (const reply of [json({ error: "no" }, 403), new Error("offline")]) {
    const g = gateway((method) => (method === "GET" ? json({ artifacts: [] }) : reply));
    const m = await mount(g);
    await m.run((a) => a.deleteArtifact("a.txt"));
    await m.run((a) => a.pinArtifact("a.txt", false));
    assert.deepEqual(m.statuses, [
      ["could not delete the artifact", true],
      ["could not pin the artifact", true],
    ]);
    assert.equal(g.calls.filter((c) => c.method === "GET").length, 0, "no reload");
    await m.done();
  }
});

test("unpinning reloads the list", async () => {
  const g = gateway((method) => (method === "POST" ? json({}) : json({ artifacts: [LIST[1]] })));
  const m = await mount(g);
  await m.run((a) => a.openArtifacts());
  await click(byText(m.root, "Unpin"));
  await settle();
  const pin = g.calls.find((c) => c.method === "POST");
  assert.deepEqual(JSON.parse(pin.init.body), { pinned: false });
  assert.equal(g.calls.at(-1).method, "GET");
  await m.done();
});

test("a picked file is uploaded into the session, and the list comes back on the session", async () => {
  const g = gateway((method, url) =>
    method === "PUT" ? json({}) : json({ artifacts: url.includes("scope") ? [] : [LIST[0]] }),
  );
  const m = await mount(g);
  await m.run((a) => a.openArtifacts());
  await click(m.root.querySelectorAll("[role=tab]")[1]);
  await settle();
  const input = m.root.querySelector("[data-testid=artifact-upload]");
  const file = new File(["hello"], "my notes.txt", { type: "text/plain" });
  Object.defineProperty(input, "files", { value: [file], configurable: true });
  await act(() => {
    input.dispatchEvent(new W.Event("change", { bubbles: true }));
  });
  await settle();
  const put = g.calls.find((c) => c.method === "PUT");
  assert.equal(put.url, "/v1/sessions/s%201/artifacts/my%20notes.txt");
  assert.equal(put.init.body, file);
  assert.equal(input.value, "", "the field is cleared for the next pick");
  assert.equal(g.calls.at(-1).url, "/v1/sessions/s%201/artifacts");
  assert.equal(m.root.querySelector("[role=tab][aria-selected=true]").textContent, "This session");
  // A change with no file picked (a cancelled dialog) uploads nothing.
  Object.defineProperty(input, "files", { value: [], configurable: true });
  await act(() => {
    input.dispatchEvent(new W.Event("change", { bubbles: true }));
  });
  await settle();
  assert.equal(g.calls.filter((c) => c.method === "PUT").length, 1);
  await m.done();
});

test("a refused upload shows the gateway's reason, or a plain one", async () => {
  const cases = [
    [json({ error: "file too large" }, 413), "file too large"],
    [new Response("not json", { status: 500 }), "could not save the file"],
    [json({}, 400), "could not save the file"],
    [new Error("offline"), "could not save the file"],
  ];
  for (const [reply, said] of cases) {
    const g = gateway(() => reply);
    const m = await mount(g);
    await m.run((a) => a.uploadArtifact(new File(["x"], "x.bin")));
    assert.deepEqual(m.statuses, [[said, true]]);
    assert.equal(g.calls.length, 1, "no reload after a refused upload");
    await m.done();
  }
});

test("a run's file opens over the session's list; the modal closes by its button or backdrop", async () => {
  const g = gateway((_, url) =>
    url.endsWith("/artifacts") ? json({ artifacts: [LIST[0]] }) : new Response("run output"),
  );
  const m = await mount(g);
  await m.run((a) => a.showRunArtifact({ name: "out.txt", type: "text/plain" }));
  assert.equal(m.hook.current.showArtifacts, true);
  assert.equal(m.hook.current.artifactScope, "session");
  assert.equal(m.root.querySelector("pre").textContent, "run output");
  assert.deepEqual(g.calls.map((c) => c.url).sort(), [
    "/v1/sessions/s%201/artifacts",
    "/v1/sessions/s%201/artifacts/out.txt",
  ]);
  await click(m.root.querySelector("button[aria-label=Close]"));
  assert.equal(m.root.innerHTML, "");
  await m.run((a) => a.openArtifacts());
  await click(m.root.firstElementChild);
  assert.equal(m.hook.current.showArtifacts, false);
  // Clicking a row opens that file.
  await m.run((a) => a.openArtifacts());
  await click(rows(m.root)[0].querySelector("button"));
  await settle();
  assert.equal(g.calls.at(-1).url, "/v1/sessions/s%201/artifacts/report.md");
  assert.equal(m.hook.current.artifactView.name, "report.md");
  await m.done();
});
