// Settings → Security lists the service containers Motita started and stops them. It is a
// convenience: a gateway without the API, a machine without a container tool or a failed first
// read all draw nothing.
// Run with: node --test web/src/__tests__/boxes.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  installDom,
  load,
  fakeApi,
  json,
  text,
  settle,
  click,
} from "./cov-settings-helpers.mjs";

installDom();
const here = dirname(fileURLToPath(import.meta.url));
const { BoxesSetting } = await load("BoxesSetting.tsx");
const { SecuritySettings } = await load("SecuritySettings.tsx");
const boxesApi = await import("../boxesApi.ts");
const es = (await import("../i18n.es.ts")).default;
const { h, render } = await import("preact");

const BOX = {
  id: "abc123def456",
  name: "motita-box-x-1",
  image: "nginx:alpine",
  status: "Up 2 minutes",
  created: "now",
};

async function mount(api, component = BoxesSetting) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(component, { api }), root);
  await settle();
  return {
    root,
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}

const q = (root, id) => root.querySelector(`[data-testid=${id}]`);

// gateway keeps a list of boxes and answers the boxes API as the Go side does.
function gateway(initial, runtime = "podman") {
  let boxes = initial;
  return fakeApi({
    "GET /v1/boxes": () => json(200, { runtime, boxes }),
    "DELETE /v1/boxes/": (path) => {
      const id = decodeURIComponent(path.split("/").pop());
      boxes = boxes.filter((b) => b.id !== id);
      return new Response(null, { status: 204 });
    },
    "DELETE /v1/boxes": () => {
      const removed = boxes.length;
      boxes = [];
      return json(200, { removed });
    },
  });
}

test("with nothing running it says so, and refresh reads again", async () => {
  const f = gateway([]);
  const m = await mount(f.api);
  assert.match(text(m.root), /Services in boxes/);
  assert.match(text(q(m.root, "boxes-empty")), /No boxes are running/);
  assert.equal(q(m.root, "boxes-stop-all"), null);
  assert.equal(q(m.root, "boxes-list"), null);
  click(q(m.root, "boxes-refresh"));
  await settle();
  assert.equal(f.calls.filter((c) => c.method === "GET").length, 2);
  await m.unmount();
});

test("each box can be stopped, and the list is read again", async () => {
  const other = { ...BOX, id: "fff000aaa111", name: "motita-box-x-2" };
  const f = gateway([BOX, other]);
  const m = await mount(f.api);
  assert.equal(m.root.querySelectorAll("[data-testid=box]").length, 2);
  assert.match(text(m.root), /motita-box-x-1/);
  assert.match(text(m.root), /nginx:alpine · Up 2 minutes/);
  const stop = m.root.querySelector("[aria-label='Stop motita-box-x-1']");
  click(stop);
  await settle();
  const del = f.calls.find((c) => c.method === "DELETE");
  assert.equal(del.path, "/v1/boxes/abc123def456");
  assert.equal(m.root.querySelectorAll("[data-testid=box]").length, 1);
  assert.doesNotMatch(text(m.root), /motita-box-x-1/);
  await m.unmount();
});

test("stop all removes every box", async () => {
  const f = gateway([BOX]);
  const m = await mount(f.api);
  click(q(m.root, "boxes-stop-all"));
  await settle();
  assert.equal(
    f.calls.filter((c) => c.method === "DELETE")[0].path,
    "/v1/boxes",
  );
  assert.ok(q(m.root, "boxes-empty"));
  await m.unmount();
});

test("a stop the gateway refuses shows its error, and a new one clears it", async () => {
  const f = fakeApi({
    "GET /v1/boxes": json(200, { runtime: "docker", boxes: [BOX] }),
    "DELETE /v1/boxes/": json(502, { error: "docker could not remove it" }),
    "DELETE /v1/boxes": json(500),
  });
  const m = await mount(f.api);
  click(q(m.root, "box-stop"));
  await settle();
  assert.match(text(q(m.root, "boxes-error")), /could not remove it/);
  click(q(m.root, "boxes-stop-all"));
  await settle();
  assert.match(text(q(m.root, "boxes-error")), /HTTP 500/);
  await m.unmount();
});

test("it draws nothing where there is nothing to show", async () => {
  for (const api of [
    fakeApi({ "GET /v1/boxes": json(404, {}) }).api,
    gateway([], "").api,
    fakeApi({ "GET /v1/boxes": json(502, { error: "daemon down" }) }).api,
    fakeApi({}).api,
  ]) {
    const m = await mount(api);
    assert.equal(m.root.textContent, "");
    await m.unmount();
  }
});

test("the Security tab carries the boxes", async () => {
  const f = fakeApi({
    "GET /v1/security": json(200, {
      disk_access: "workspace",
      can_save: true,
      enforced: true,
    }),
    "GET /v1/boxes": json(200, { runtime: "podman", boxes: [BOX] }),
  });
  const m = await mount(f.api, SecuritySettings);
  assert.ok(q(m.root, "boxes-list"));
  await m.unmount();
});

test("the client reads, removes and stops", async () => {
  const f = fakeApi({
    "GET /v1/boxes": json(200, { runtime: "podman", boxes: [] }),
    "DELETE /v1/boxes/": new Response(null, { status: 204 }),
    "DELETE /v1/boxes": json(200, { removed: 4 }),
  });
  assert.deepEqual(await boxesApi.getBoxes(f.api), {
    runtime: "podman",
    boxes: [],
  });
  await boxesApi.removeBox(f.api, "a b");
  assert.equal(f.calls[1].path, "/v1/boxes/a%20b");
  assert.equal(await boxesApi.stopBoxes(f.api), 4);
});

test("every string of the panel has a Spanish translation", () => {
  const source = readFileSync(join(here, "..", "BoxesSetting.tsx"), "utf8");
  const keys = new Set();
  for (const m of source.matchAll(
    /\bt[fc]?\(\s*(['"])((?:(?!\1)[^\\]|\\.)*)\1/g,
  )) {
    keys.add(m[2]);
  }
  const MIN_STRINGS = 6;
  assert.ok(keys.size >= MIN_STRINGS, String(keys.size));
  assert.deepEqual(
    [...keys].filter((k) => !es[k]),
    [],
  );
});
