// Files attached to a message: the upload, the text the agent receives, and the composer's
// button, chips, paste and drop.
// Run with: node --test web/src/__tests__/cov-attachments.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import {
  act,
  h,
  json,
  load,
  mount,
  renderHook,
  text,
} from "./cov-chat-helpers.mjs";

const A = await load("attachments.ts");
const { useComposer } = await load("useComposer.ts");
const { Composer } = await load("Composer.tsx");
const { AppCtx } = await load("appStore.ts");

// fakeApi answers uploads from a table of handlers and records them: the body is a file, not JSON.
function fakeApi(route) {
  const calls = [];
  const api = async (path, init) => {
    calls.push({ path, init });
    return (await route(path, init)) ?? json({ error: "not found" }, 404);
  };
  api.calls = calls;
  return api;
}

const shot = {
  name: "shot.png",
  path: ".motita/uploads/shot.png",
  size: 2048,
  kind: "image",
};
const demo = {
  name: "demo.mp4",
  path: ".motita/uploads/demo.mp4",
  size: 5 * 1024 * 1024,
  kind: "video",
};
const file = (name, size = 3, type = "") => ({ name, size, type });

test("sizes read the way a person says them", () => {
  assert.equal(A.sizeLabel(10), "10 B");
  assert.equal(A.sizeLabel(2048), "2 KB");
  assert.equal(A.sizeLabel(5 * 1024 * 1024), "5.0 MB");
});

test("the message names each file with a hint for its kind, and ", () => {
  assert.equal(A.withAttachments("hi", []), "hi");
  const sent = A.withAttachments("what is this?", [
    shot,
    demo,
    { name: "a.mp3", path: "p/a.mp3", size: 1, kind: "audio" },
    { name: "a.pdf", path: "p/a.pdf", size: 1, kind: "document" },
    { name: "a.go", path: "p/a.go", size: 1, kind: "file" },
  ]);
  assert.match(sent, /^what is this\?\n\n\[The person attached these files/);
  assert.match(
    sent,
    /- \.motita\/uploads\/shot\.png \(image, 2 KB\) - view it with an image tool/,
  );
  assert.match(
    sent,
    /demo\.mp4 \(video, 5\.0 MB\) - sample frames with ffmpeg/,
  );
  assert.match(sent, /transcribe/);
  assert.match(sent, /pdftotext/);
  assert.match(sent, /a\.go \(file, 1 B\)\]$/);
  assert.match(A.withAttachments("", [shot]), /^Look at the attached files/);
});

test("a drag or a paste yields its files", () => {
  assert.deepEqual(A.filesOf(null), []);
  assert.deepEqual(A.filesOf({}), []);
  assert.deepEqual(A.filesOf({ files: [1, 2] }), [1, 2]);
});

test("an upload sends the bytes with the file's name and type, and reports why it failed", async () => {
  const api = fakeApi(
    (path) => path.includes("/attachments") && json(shot, 201),
  );
  const f = file("my shot.png", 3, "image/png");
  assert.deepEqual(await A.uploadAttachment(api, "s 1", f), shot);
  const call = api.calls[0];
  assert.equal(call.path, "/v1/sessions/s%201/attachments?name=my%20shot.png");
  assert.equal(call.init.method, "POST");
  assert.equal(call.init.headers["Content-Type"], "image/png");
  assert.equal(call.init.body, f);

  await A.uploadAttachment(api, "s1", file("", 3));
  assert.match(api.calls[1].path, /name=file$/);
  assert.equal(
    api.calls[1].init.headers["Content-Type"],
    "application/octet-stream",
  );

  const refusing = fakeApi(() => json({ error: "too many" }, 409));
  await assert.rejects(
    A.uploadAttachment(refusing, "s1", file("a.txt")),
    /a\.txt: too many/,
  );
  const bare = fakeApi(() => new Response("oops", { status: 500 }));
  await assert.rejects(
    A.uploadAttachment(bare, "s1", file("a.txt")),
    /a\.txt: HTTP 500/,
  );
  await assert.rejects(
    A.uploadAttachment(api, "s1", file("big.mp4", A.MAX_ATTACHMENT_BYTES + 1)),
    /big\.mp4: larger than 200\.0 MB/,
  );
});

async function composer({ running = false, session = "s1", api } = {}) {
  const calls = [];
  const rs = {
    runningRef: { current: running },
    sessionRef: { current: session },
    setMessages() {},
  };
  const uploads = api || fakeApi(() => json(shot, 201));
  const hook = await renderHook(() =>
    useComposer({
      api: uploads,
      rs,
      drawers: { showAgents() {} },
      stream: {
        submit: (...a) => calls.push(["submit", ...a]),
        sendWhileRunning: (t) => calls.push(["while", t]),
        stopRun() {},
      },
      runShellLine: async (c) => calls.push(["shell", c]),
      startSession: async () => {
        calls.push(["start"]);
        rs.sessionRef.current = "new";
        return "new";
      },
      nextSuggestion: "",
    }),
  );
  const c = () => hook.result.current;
  const run = (fn) => act(() => fn(c()));
  return { hook, c, run, calls, rs, uploads };
}

test("attached files wait above the field and go out with the message", async () => {
  const { c, run, calls, uploads } = await composer();
  await run((x) => x.attachFiles([file("shot.png")]));
  assert.deepEqual(c().attachments, [shot]);
  assert.equal(c().uploading, 0);
  assert.equal(
    uploads.calls[0].path.startsWith("/v1/sessions/s1/attachments"),
    true,
  );
  await run((x) => x.onInput("what is this?"));
  await run((x) => x.handleSubmit({ preventDefault() {} }));
  assert.equal(calls[0][0], "submit");
  assert.match(calls[0][1], /^what is this\?\n\n\[The person attached/);
  assert.deepEqual(c().attachments, [], "the composer is empty once sent");
});

test("a message can be only files, and a plain one carries nothing extra", async () => {
  const { c, run, calls } = await composer();
  await run((x) => x.attachFiles([file("shot.png")]));
  await run((x) => x.sendQueued());
  assert.match(calls[0][1], /^Look at the attached files/);
  await run((x) => x.onInput("plain"));
  await run((x) => x.sendQueued());
  assert.deepEqual(calls[1], ["submit", "plain"]);
  await run((x) => x.sendQueued());
  assert.equal(
    calls.length,
    2,
    "nothing typed and nothing attached sends nothing",
  );
  assert.equal(c().input, "");
});

test("files with a shell-looking line are text for the agent, not a command", async () => {
  const { run, calls } = await composer();
  await run((x) => x.attachFiles([file("shot.png")]));
  await run((x) => x.onInput("!ls"));
  await run((x) => x.sendQueued());
  assert.equal(calls[0][0], "submit");
});

test("files go to the run in flight and open a conversation when there is none", async () => {
  const busy = await composer({ running: true });
  await busy.run((x) => x.attachFiles([file("shot.png")]));
  await busy.run((x) => x.sendQueued());
  assert.equal(busy.calls[0][0], "while");
  assert.match(busy.calls[0][1], /shot\.png/);

  const none = await composer({ session: "" });
  await none.run((x) => x.attachFiles([file("shot.png")]));
  assert.deepEqual(none.calls[0], ["start"]);
  assert.match(none.uploads.calls[0].path, /\/v1\/sessions\/new\/attachments/);
  await none.run((x) => x.sendQueued());
  assert.equal(none.calls[1][0], "submit");
});

test("a file that fails says why, and the others still arrive", async () => {
  let n = 0;
  const api = fakeApi(() =>
    n++ === 0 ? json({ error: "full" }, 409) : json(demo, 201),
  );
  const { c, run } = await composer({ api });
  await run((x) => x.attachFiles([file("a.txt"), file("demo.mp4")]));
  assert.deepEqual(c().attachments, [demo]);
  assert.equal(c().attachError, "a.txt: full");
  await run((x) => x.attachFiles([]));
  assert.equal(
    c().attachError,
    "a.txt: full",
    "nothing to attach changes nothing",
  );
  await run((x) => x.attachFiles([file("demo.mp4")]));
  assert.equal(c().attachError, "", "a new attempt clears the old message");
  await run((x) => x.removeAttachment(demo.path));
  assert.deepEqual(c().attachments, []);
});

test("no conversation could be opened: nothing is uploaded", async () => {
  const { c, run, uploads, rs } = await composer({ session: "" });
  const hook = await renderHook(() =>
    useComposer({
      api: uploads,
      rs,
      drawers: {},
      stream: {},
      runShellLine: async () => {},
      startSession: async () => undefined,
      nextSuggestion: "",
    }),
  );
  await act(() => hook.result.current.attachFiles([file("a.txt")]));
  assert.equal(uploads.calls.length, 0);
  assert.deepEqual(hook.result.current.attachments, []);
  assert.equal(c().uploading, 0);
});

// ── The composer's button, chips, paste and drop ─────────────────────────────

function view(over = {}) {
  const calls = [];
  const value = {
    composer: {
      input: "",
      slashPopup: null,
      activeTags: [],
      shellMode: false,
      nextSuggestion: "",
      attachments: [],
      uploading: 0,
      attachError: "",
      attachFiles: async (f) => calls.push(["attach", f.length]),
      removeAttachment: (p) => calls.push(["remove", p]),
      onInput() {},
      handleKeydown() {},
      handleSubmit: (e) => e.preventDefault(),
      removeTag() {},
      pickSlash() {},
      setSlashPopup() {},
      ...over.composer,
    },
    isMerged: false,
    voice: { phase: "idle", listening: false, showMic: false },
    ...over.top,
  };
  return { calls, value };
}

async function shown(over) {
  const v = view(over);
  const m = await mount(
    h(AppCtx.Provider, { value: v.value }, h(Composer, {})),
  );
  return { ...v, m, q: (s) => m.root.querySelector(s) };
}

test("the paperclip opens the file picker, and chosen files are attached", async () => {
  const c = await shown();
  const input = c.q("[data-testid=attach-input]");
  let opened = 0;
  input.click = () => opened++;
  c.q("[data-testid=composer-attach]").click();
  assert.equal(opened, 1);
  Object.defineProperty(input, "files", {
    value: [file("a"), file("b")],
    configurable: true,
  });
  await act(() =>
    input.dispatchEvent(new window.Event("change", { bubbles: true })),
  );
  assert.deepEqual(c.calls, [["attach", 2]]);
});

test("a merged session cannot take attachments", async () => {
  const c = await shown({ top: { isMerged: true } });
  assert.equal(c.q("[data-testid=composer-attach]").disabled, true);
});

test("attached files show as chips that can be taken off, with an uploading note and an error", async () => {
  const c = await shown({
    composer: {
      attachments: [shot, demo],
      uploading: 1,
      attachError: "a.txt: full",
    },
  });
  const chips = [...c.m.root.querySelectorAll("[data-testid=attachment-chip]")];
  assert.equal(chips.length, 2);
  assert.equal(chips[0].title, "shot.png (2 KB)");
  chips[1].querySelector("button").click();
  assert.deepEqual(c.calls, [["remove", demo.path]]);
  assert.equal(text(c.q("[data-testid=attach-busy]")), "Uploading…");
  assert.equal(text(c.q("[data-testid=attach-error]")), "a.txt: full");
  assert.equal(
    c.q("button[type=submit]").disabled,
    true,
    "send waits for the upload",
  );
  const idle = await shown();
  assert.equal(idle.q("[data-testid=attachment-chip]"), null);
  assert.equal(idle.q("[data-testid=attach-busy]"), null);
  assert.equal(idle.q("[data-testid=attach-error]"), null);
});

test("dropping files attaches them, and pasting a file does too but pasting text does not", async () => {
  const c = await shown();
  const form = c.q("form");
  const over = new window.Event("dragover", {
    bubbles: true,
    cancelable: true,
  });
  form.dispatchEvent(over);
  assert.equal(over.defaultPrevented, true);
  const drop = new window.Event("drop", { bubbles: true, cancelable: true });
  drop.dataTransfer = { files: [file("a")] };
  await act(() => form.dispatchEvent(drop));
  assert.equal(drop.defaultPrevented, true);
  assert.deepEqual(c.calls, [["attach", 1]]);

  const field = c.q("textarea");
  const paste = new window.Event("paste", { bubbles: true, cancelable: true });
  paste.clipboardData = { files: [file("img.png")] };
  await act(() => field.dispatchEvent(paste));
  assert.equal(paste.defaultPrevented, true);
  const word = new window.Event("paste", { bubbles: true, cancelable: true });
  word.clipboardData = { files: [] };
  await act(() => field.dispatchEvent(word));
  assert.equal(word.defaultPrevented, false);
  assert.deepEqual(c.calls, [
    ["attach", 1],
    ["attach", 1],
  ]);
});

test("a message's files are read back from its text, to be served by their session", () => {
  const sent = A.withAttachments("what is this?", [
    {
      name: "my (1).png",
      path: ".motita/uploads/s1/my (1).png",
      size: 3,
      kind: "image",
    },
    {
      name: "a.pdf",
      path: ".motita/uploads/s1/a.pdf",
      size: 1,
      kind: "document",
    },
  ]);
  const { lead, files } = A.attachedIn(sent);
  assert.equal(lead, "what is this?");
  assert.deepEqual(files, [
    {
      name: "my (1).png",
      kind: "image",
      url: "/v1/sessions/s1/attachments/my%20(1).png",
    },
    {
      name: "a.pdf",
      kind: "document",
      url: "/v1/sessions/s1/attachments/a.pdf",
    },
  ]);
  assert.deepEqual(A.attachedIn("just text"), { lead: "just text", files: [] });
  const odd =
    "x\n\n[The person attached these files, saved in your workspace:\nnot a file line\n- elsewhere/a.png (image, 1 B)]";
  assert.deepEqual(A.attachedIn(odd), { lead: "x", files: [] });
});

test("only a stored picture has a thumbnail", () => {
  assert.equal(
    A.thumbOf({
      name: "a.png",
      path: ".motita/uploads/s1/a.png",
      size: 1,
      kind: "image",
    }),
    "/v1/sessions/s1/attachments/a.png",
  );
  assert.equal(
    A.thumbOf({
      name: "a.pdf",
      path: ".motita/uploads/s1/a.pdf",
      size: 1,
      kind: "document",
    }),
    "",
  );
  assert.equal(
    A.thumbOf({
      name: "a.png",
      path: "elsewhere/a.png",
      size: 1,
      kind: "image",
    }),
    "",
  );
});

test("a message of the user's with files draws its text, thumbnails and names", async () => {
  const { UserText } = await load("ConversationParts.tsx");
  const sent = A.withAttachments("look", [
    { name: "a.png", path: ".motita/uploads/s1/a.png", size: 1, kind: "image" },
    {
      name: "a.pdf",
      path: ".motita/uploads/s1/a.pdf",
      size: 1,
      kind: "document",
    },
  ]);
  const m = await mount(
    h(UserText, { m: { id: 1, role: "user", text: sent } }),
  );
  const img = m.root.querySelector("img.msg-thumb");
  assert.equal(img.getAttribute("src"), "/v1/sessions/s1/attachments/a.png");
  assert.equal(
    img.closest("a").getAttribute("href"),
    "/v1/sessions/s1/attachments/a.png",
  );
  assert.equal(text(m.root.querySelector(".msg-file")), "📎 a.pdf");
  assert.match(text(m.root), /^look/);
  const plain = await mount(
    h(UserText, { m: { id: 2, role: "user", text: "hi" } }),
  );
  assert.equal(plain.root.querySelector(".msg-files"), null);
});

test("a picture's chip shows its thumbnail", async () => {
  const c = await shown({ composer: { attachments: [shot, demo] } });
  assert.equal(
    c.m.root.querySelectorAll("[data-testid=attachment-chip] img").length,
    0,
  );
  const pic = await shown({
    composer: {
      attachments: [
        {
          name: "a.png",
          path: ".motita/uploads/s1/a.png",
          size: 1,
          kind: "image",
        },
      ],
    },
  });
  assert.equal(
    pic.q("[data-testid=attachment-chip] img").getAttribute("src"),
    "/v1/sessions/s1/attachments/a.png",
  );
});
