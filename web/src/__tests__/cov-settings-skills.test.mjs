// The skill library through the real useSkills hook: the list and its filter, a skill opened in
// full with its pin, on/off and delete, the proposals the review made, the archive, and every
// refusal the gateway can answer. Markdown (another group's file) is stood in for by a stub.
// Run with: node --test web/src/__tests__/cov-settings-skills.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  load,
  stub,
  json,
  text,
  settle,
  click,
  type,
  byText,
} from "./cov-settings-helpers.mjs";

installDom();
stub("./Markdown", `
export function Markdown({ content }) {
  return <div data-testid="md">{content}</div>;
}
`);
const { useSkills, skillsPathFor } = await load("useSkills.ts");
const { SkillLibraryModal } = await load("SkillLibraryModal.tsx");
const { AppCtx } = await load("appStore.ts");
const { h, render } = await import("preact");

const SKILLS = [
  { name: "deploy", title: "Deploy the app", summary: "how to ship", path: "", created_by: "agent", state: "stale", pinned: true, use_count: 4, view_count: 0, patch_count: 0 },
  { name: "lint", title: "", summary: "", path: "", created_by: "foreground", state: "active", disabled: true, use_count: 0, view_count: 0, patch_count: 0 },
];

// exactApi answers "METHOD path" exactly from a table; a value can be a function of the call.
function exactApi(table) {
  const calls = [];
  const api = async (path, init = {}) => {
    const method = init.method || "GET";
    calls.push({ method, path, body: init.body ? JSON.parse(init.body) : undefined });
    const k = method + " " + path;
    if (!(k in table)) {
      throw new Error("unrouted " + k);
    }
    let r = table[k];
    if (typeof r === "function") {
      r = await r();
    }
    if (r instanceof Error) {
      throw r;
    }
    return r.clone();
  };
  return { api, calls, table };
}

function library(base = "/v1/skills/", extra = {}) {
  return {
    ["GET " + base.slice(0, -1)]: json(200, { skills: SKILLS }),
    ["GET " + base + "archived"]: json(200, { skills: ["old-one"] }),
    ["GET " + base + "proposed"]: json(200, { skills: ["new-idea"] }),
    ...extra,
  };
}

function mount(api, sessionId = "") {
  const states = [];
  const deletes = [];
  const ref = {};
  function Harness() {
    const skills = useSkills(api, sessionId, (s, bad) => states.push([s, bad]));
    ref.s = skills;
    const deletion = { setConfirmDelete: (x) => deletes.push(x) };
    return h(AppCtx.Provider, { value: { skills, deletion } }, skills.showSkillLibrary ? h(SkillLibraryModal, null) : null);
  }
  const root = document.createElement("div");
  document.body.appendChild(root);
  render(h(Harness, null), root);
  return {
    root,
    states,
    deletes,
    ref,
    async open() {
      await settle();
      ref.s.setShowSkillLibrary(true);
      await settle();
    },
    item: (label) => [...root.querySelectorAll("li")].find((l) => text(l).startsWith(label)),
    async unmount() {
      render(null, root);
      root.remove();
      await settle();
    },
  };
}
async function press(el) {
  click(el);
  await settle();
}
const btn = (root, label) => byText(root, "button", label);
const heading = (root) => text(root.querySelector("h2"));

test("skillsPathFor is the session's own door, or the shared shelf", () => {
  assert.equal(skillsPathFor("a b"), "/v1/sessions/a%20b/skills/");
  assert.equal(skillsPathFor(""), "/v1/skills/");
});

test("opening the library loads its three lists; the filter runs over what was loaded", async () => {
  const f = exactApi(library());
  const m = mount(f.api);
  await settle();
  assert.equal(f.calls.length, 0);
  await m.open();
  assert.deepEqual(f.calls.map((c) => c.path), ["/v1/skills", "/v1/skills/archived", "/v1/skills/proposed"]);
  assert.match(heading(m.root), /./);
  const deploy = m.item("Deploy the app");
  assert.match(text(deploy), /agent/);
  assert.match(text(deploy), /stale/);
  assert.match(text(deploy), /pinned/);
  assert.match(text(deploy), /used 4×/);
  assert.match(text(deploy), /how to ship/);
  const lint = m.item("lint");
  assert.equal(lint.querySelector("p"), null);
  assert.doesNotMatch(text(lint), /agent|stale|pinned/);
  assert.ok(lint.querySelector(".skill-tag-off"));
  assert.equal(lint.querySelectorAll("button")[1].title, "The agent will see this again");
  assert.equal(deploy.querySelectorAll("button")[1].title, "The agent stops seeing this in its index and its search");
  assert.match(text(m.root), /Proposed by the review/);
  assert.ok(m.item("new-idea"));
  assert.match(text(m.root), /Archived/);
  assert.ok(m.item("old-one"));

  const filter = m.root.querySelector("input[type=text]");
  type(filter, "  SHIP ");
  await settle();
  assert.ok(m.item("Deploy the app"));
  assert.equal(m.item("lint"), undefined);
  type(filter, "zzz");
  await settle();
  assert.match(text(m.root), /Nothing matches “zzz”\./);
  // Closing and reopening resets the filter and reloads.
  await press(btn(m.root, "Close"));
  assert.equal(m.root.innerHTML, "");
  await m.open();
  assert.equal(m.root.querySelector("input[type=text]").value, "");
  assert.equal(f.calls.length, 6);
  await m.unmount();
});

test("an empty library says so; a session's library is asked at its own door", async () => {
  const base = "/v1/sessions/s%201/skills/";
  const f = exactApi({
    ["GET " + base.slice(0, -1)]: json(200, {}),
    ["GET " + base + "archived"]: json(200, {}),
    ["GET " + base + "proposed"]: json(200, {}),
  });
  const m = mount(f.api, "s 1");
  await m.open();
  assert.deepEqual(f.calls.map((c) => c.path), [base.slice(0, -1), base + "archived", base + "proposed"]);
  assert.match(text(m.root), /No skills yet\./);
  assert.doesNotMatch(text(m.root), /Proposed by the review|Archived/);
  await m.unmount();
});

test("while it loads it says so; refused or failed loads are reported", async () => {
  let answer;
  const f = exactApi(library("/v1/skills/", { "GET /v1/skills": () => new Promise((r) => (answer = r)) }));
  const m = mount(f.api);
  await m.open();
  assert.match(text(m.root), /Loading the library…/);
  answer(json(200, { skills: [] }));
  await settle();
  assert.doesNotMatch(text(m.root), /Loading/);
  await m.unmount();

  const g = exactApi(library("/v1/skills/", {
    "GET /v1/skills": json(500, {}),
  }));
  const n = mount(g.api);
  await n.open();
  assert.deepEqual(n.states, [["could not load the skill library", true]]);
  assert.doesNotMatch(text(n.root), /Loading/);
  await n.unmount();

  const k = exactApi(library("/v1/skills/", { "GET /v1/skills/archived": new Error("offline") }));
  const o = mount(k.api);
  await o.open();
  assert.deepEqual(o.states, [["could not load the skill library", true]]);
  await o.unmount();

  // Archive and proposals that cannot be read are left out.
  const p = exactApi(library("/v1/skills/", {
    "GET /v1/skills/archived": json(404, {}),
    "GET /v1/skills/proposed": json(404, {}),
  }));
  const q = mount(p.api);
  await q.open();
  assert.ok(q.item("Deploy the app"));
  assert.doesNotMatch(text(q.root), /Proposed by the review|Archived/);
  assert.deepEqual(q.states, []);
  await q.unmount();
});

test("a skill opens in full, with its pin, its switch and delete, and goes back to the list", async () => {
  const f = exactApi(library("/v1/skills/", {
    "GET /v1/skills/deploy": json(200, { body: "# Deploy\nsteps" }),
    "GET /v1/skills/lint": json(200, {}),
    "POST /v1/skills/deploy/pin": json(200, {}),
    "POST /v1/skills/deploy/disable": json(200, {}),
  }));
  const m = mount(f.api);
  await m.open();
  await press(m.item("Deploy the app").querySelector("button"));
  assert.equal(text(m.root.querySelector("[data-testid=md]")), "# Deploy\nsteps".replace(/\s+/g, " "));
  assert.match(text(m.root), /deploy/);
  assert.match(text(m.root), /used 4×/);
  const unpin = btn(m.root, "Unpin");
  assert.equal(unpin.title, "The curator will leave this alone");
  await press(unpin);
  assert.deepEqual(f.calls.at(-1), { method: "POST", path: "/v1/skills/deploy/pin", body: { pinned: false } });
  const pin = btn(m.root, "Pin");
  assert.equal(pin.title, "Exempt this from every automatic transition");
  await press(btn(m.root, "Turn off"));
  assert.deepEqual(f.calls.at(-1).body, { disabled: true });
  assert.ok(btn(m.root, "Turn on"));
  await press(btn(m.root, "Delete"));
  assert.deepEqual(m.deletes, [{ type: "skill", id: "deploy", title: "Deploy the app" }]);

  // Back to the list: the list kept the changes.
  await press(btn(m.root, "Back to the list"));
  const deploy = m.item("Deploy the app");
  assert.doesNotMatch(text(deploy), /pinned/);
  assert.ok(deploy.querySelector(".skill-tag-off"));

  // A skill with no title is named by its name; one with no body shows an empty page.
  await press(m.item("lint").querySelector("button"));
  assert.equal(text(m.root.querySelector("[data-testid=md]")), "");
  await press(btn(m.root, "Delete"));
  assert.deepEqual(m.deletes.at(-1), { type: "skill", id: "lint", title: "lint" });
  // Close from the open skill closes the library.
  const closes = [...m.root.querySelectorAll("button")].filter((b) => text(b) === "Close");
  assert.equal(closes.length, 1);
  await press(closes[0]);
  assert.equal(m.root.innerHTML, "");
  await m.unmount();
});

test("the switch on a list row turns a skill on or off without opening it", async () => {
  const f = exactApi(library("/v1/skills/", {
    "POST /v1/skills/lint/disable": json(200, {}),
  }));
  const m = mount(f.api);
  await m.open();
  await press(m.item("lint").querySelectorAll("button")[1]);
  assert.deepEqual(f.calls.at(-1), { method: "POST", path: "/v1/skills/lint/disable", body: { disabled: false } });
  assert.equal(m.item("lint").querySelector(".skill-tag-off"), null);
  await m.unmount();
});

test("refused or failed actions on a skill are reported and change nothing", async () => {
  const f = exactApi(library("/v1/skills/", {
    "GET /v1/skills/deploy": json(200, { body: "b" }),
    "GET /v1/skills/lint": json(404, { error: "no such skill" }),
    "POST /v1/skills/deploy/pin": json(500, {}),
    "POST /v1/skills/deploy/disable": json(409, { error: "pinned skills stay on" }),
    "POST /v1/skills/lint/disable": json(500, {}),
  }));
  const m = mount(f.api);
  await m.open();
  await press(m.item("lint").querySelector("button"));
  assert.deepEqual(m.states.at(-1), ["no such skill", true]);
  assert.ok(m.item("lint"));
  await press(m.item("lint").querySelectorAll("button")[1]);
  assert.deepEqual(m.states.at(-1), ["could not change the skill", true]);
  await press(m.item("Deploy the app").querySelector("button"));
  await press(btn(m.root, "Unpin"));
  assert.deepEqual(m.states.at(-1), ["could not change the pin", true]);
  assert.ok(btn(m.root, "Unpin"));
  await press(btn(m.root, "Turn off"));
  assert.deepEqual(m.states.at(-1), ["pinned skills stay on", true]);
  assert.ok(btn(m.root, "Turn off"));
  await m.unmount();

  // Unreachable gateway.
  const g = exactApi(library("/v1/skills/", {
    "GET /v1/skills/deploy": json(200, { body: "b" }),
    "GET /v1/skills/lint": new Error("offline"),
    "POST /v1/skills/deploy/pin": new Error("offline"),
    "POST /v1/skills/deploy/disable": new Error("offline"),
    "GET /v1/skills/zzz": new Response("x", { status: 500 }),
  }));
  const n = mount(g.api);
  await n.open();
  await press(n.item("lint").querySelector("button"));
  assert.deepEqual(n.states.at(-1), ["could not open the skill", true]);
  await n.ref.s.openSkill({ name: "zzz" });
  assert.deepEqual(n.states.at(-1), ["could not open the skill", true]);
  await press(n.item("Deploy the app").querySelector("button"));
  await press(btn(n.root, "Unpin"));
  assert.deepEqual(n.states.at(-1), ["could not change the pin", true]);
  await press(btn(n.root, "Turn off"));
  assert.deepEqual(n.states.at(-1), ["could not change the skill", true]);
  await n.unmount();
});

test("pin and switch on a closed skill only patch the list", async () => {
  const f = exactApi(library("/v1/skills/", {
    "POST /v1/skills/deploy/pin": json(200, {}),
    "POST /v1/skills/deploy/disable": json(200, {}),
  }));
  const m = mount(f.api);
  await m.open();
  await m.ref.s.pinSkill("deploy", false);
  await m.ref.s.disableSkill("deploy", true);
  await settle();
  assert.equal(m.ref.s.skillOpen, null);
  const d = m.ref.s.filteredSkills.find((s) => s.name === "deploy");
  assert.equal(d.pinned, false);
  assert.equal(d.disabled, true);
  await m.unmount();
});

test("delete removes a skill and reloads; a refusal or failure is reported", async () => {
  let listed = SKILLS;
  const answers = [json(409, { error: "in use" }), json(500, {}), new Error("offline"), json(200, {})];
  const f = exactApi(library("/v1/skills/", {
    "GET /v1/skills": () => json(200, { skills: listed }),
    "DELETE /v1/skills/lint": () => {
      const a = answers.shift();
      if (!(a instanceof Error) && a.ok) {
        listed = SKILLS.slice(0, 1);
      }
      return a;
    },
  }));
  const m = mount(f.api);
  await m.open();
  await m.ref.s.deleteSkill("lint");
  assert.deepEqual(m.states.at(-1), ["in use", true]);
  await m.ref.s.deleteSkill("lint");
  assert.deepEqual(m.states.at(-1), ["could not delete the skill", true]);
  await m.ref.s.deleteSkill("lint");
  assert.deepEqual(m.states.at(-1), ["could not delete the skill", true]);
  await m.ref.s.deleteSkill("lint");
  await settle();
  assert.equal(m.item("lint"), undefined);
  assert.equal(m.states.length, 3);
  await m.unmount();
});

test("a proposal is read before it is accepted or rejected, and the lists reload", async () => {
  let proposed = ["new-idea", "other"];
  const f = exactApi(library("/v1/skills/", {
    "GET /v1/skills/proposed": () => json(200, { skills: proposed }),
    "GET /v1/skills/proposed/new-idea": json(200, { body: "the idea" }),
    "GET /v1/skills/proposed/other": json(200, {}),
    "POST /v1/skills/proposed/new-idea/accept": () => ((proposed = ["other"]), json(200, {})),
    "DELETE /v1/skills/proposed/other": () => ((proposed = []), json(200, {})),
  }));
  const m = mount(f.api);
  await m.open();
  const toggle = () => m.item("new-idea").querySelector("button");
  assert.equal(toggle().getAttribute("aria-expanded"), "false");
  await press(toggle());
  assert.equal(toggle().getAttribute("aria-expanded"), "true");
  assert.equal(text(m.item("new-idea").querySelector("[data-testid=md]")), "the idea");
  // A second click folds it.
  await press(toggle());
  assert.equal(m.item("new-idea").querySelector("[data-testid=md]"), null);
  // Open another, then accept this one: the open one stays open.
  await press(m.item("other").querySelector("button"));
  assert.equal(text(m.item("other").querySelector("[data-testid=md]")), "");
  await press(btn(m.item("new-idea"), "Accept"));
  assert.deepEqual(f.calls.find((c) => c.method === "POST"), { method: "POST", path: "/v1/skills/proposed/new-idea/accept", body: undefined });
  assert.equal(m.item("new-idea"), undefined);
  assert.ok(m.item("other").querySelector("[data-testid=md]"));
  // Rejecting the open one closes it.
  await press(btn(m.item("other"), "Reject"));
  assert.equal(m.ref.s.proposalOpen, null);
  assert.doesNotMatch(text(m.root), /Proposed by the review/);
  await m.unmount();
});

test("proposals that cannot be read, accepted or rejected are reported", async () => {
  const f = exactApi(library("/v1/skills/", {
    "GET /v1/skills/proposed/new-idea": json(404, { error: "gone" }),
    "GET /v1/skills/proposed/b": json(500, {}),
    "GET /v1/skills/proposed/c": new Error("offline"),
    "POST /v1/skills/proposed/new-idea/accept": json(409, { error: "name taken" }),
    "POST /v1/skills/proposed/b/accept": json(500, {}),
    "DELETE /v1/skills/proposed/new-idea": json(500, {}),
    "DELETE /v1/skills/proposed/c": new Error("offline"),
  }));
  const m = mount(f.api);
  await m.open();
  await press(m.item("new-idea").querySelector("button"));
  assert.deepEqual(m.states.at(-1), ["gone", true]);
  await m.ref.s.openProposal("b");
  assert.deepEqual(m.states.at(-1), ["could not open the proposed skill", true]);
  await m.ref.s.openProposal("c");
  assert.deepEqual(m.states.at(-1), ["could not open the proposed skill", true]);
  await press(btn(m.item("new-idea"), "Accept"));
  assert.deepEqual(m.states.at(-1), ["name taken", true]);
  await m.ref.s.decideProposal("b", true);
  assert.deepEqual(m.states.at(-1), ["could not accept the skill", true]);
  await press(btn(m.item("new-idea"), "Reject"));
  assert.deepEqual(m.states.at(-1), ["could not reject the skill", true]);
  await m.ref.s.decideProposal("c", false);
  assert.deepEqual(m.states.at(-1), ["could not reject the skill", true]);
  assert.ok(m.item("new-idea"));
  await m.unmount();
});

test("an archived skill is restored and the lists reload; refusals are reported", async () => {
  let archived = ["old-one", "older"];
  const f = exactApi(library("/v1/skills/", {
    "GET /v1/skills/archived": () => json(200, { skills: archived }),
    "POST /v1/skills/old-one/restore": () => ((archived = ["older"]), json(200, {})),
    "POST /v1/skills/older/restore": json(409, { error: "a skill with that name exists" }),
    "POST /v1/skills/x/restore": json(500, {}),
    "POST /v1/skills/y/restore": new Error("offline"),
  }));
  const m = mount(f.api);
  await m.open();
  await press(btn(m.item("old-one"), "Restore"));
  assert.equal(m.item("old-one"), undefined);
  await press(btn(m.item("older"), "Restore"));
  assert.deepEqual(m.states.at(-1), ["a skill with that name exists", true]);
  await m.ref.s.restoreSkill("x");
  assert.deepEqual(m.states.at(-1), ["could not restore the skill", true]);
  await m.ref.s.restoreSkill("y");
  assert.deepEqual(m.states.at(-1), ["could not restore the skill", true]);
  await m.unmount();
});

test("the ✕ and the backdrop close the library", async () => {
  const f = exactApi(library());
  const m = mount(f.api);
  await m.open();
  await press(m.root.querySelector("button[aria-label=Close]"));
  assert.equal(m.ref.s.showSkillLibrary, false);
  await m.open();
  await press(m.root.querySelector("[data-modal]"));
  assert.equal(m.ref.s.showSkillLibrary, false);
  await m.unmount();
});
