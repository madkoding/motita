// The gateway's voice service in Settings → Read aloud (SpeechService.tsx), loaded as the source
// itself: turning it on with a confirmation, moving it, turning it off, a gateway that refuses, and
// the page that has no API to save it with.
import test from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  source,
  settle as settleWith,
  json,
} from "./cov-voice-helpers.mjs";

const dom = setupDom("https://localhost/");
const { SpeechService } = await source("SpeechService.tsx");
const { SpeechSettings } = await source("SpeechSettings.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const settle = () => settleWith(act, 4);
const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

async function mount(vnode) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() => render(vnode, root));
  await settle();
  return {
    root,
    tid: (id) => root.querySelector(`[data-testid=${id}]`),
    set: async (v) => {
      await act(() => render(v, root));
      await settle();
    },
    done: async () => {
      await act(() => render(null, root));
      root.remove();
    },
  };
}
const click = async (el) => {
  await act(() => el.click());
  await settle();
};
const change = async (input, checked) => {
  input.checked = checked;
  await act(() =>
    input.dispatchEvent(new dom.window.Event("change", { bubbles: true })),
  );
  await settle();
};
const type = async (input, value) => {
  input.value = value;
  await act(() =>
    input.dispatchEvent(new dom.window.Event("input", { bubbles: true })),
  );
  await settle();
};

const savedTts = (o = {}) => ({
  enabled: false,
  api: "openai",
  base_url: "",
  endpoint: "https://llm.example/v1",
  model: "gpt-4o-mini-tts",
  voice: "alloy",
  has_key: false,
  ...o,
});
const info = (tts, o = {}) => ({
  enabled: false,
  wake_phrases: [],
  max_audio_bytes: 1,
  can_save: true,
  saved: { tts },
  ...o,
});

// A gateway that answers PUT /v1/voice/tts with what it was given folded into saved, or refuses.
function gateway(start, { refuse = "" } = {}) {
  const puts = [];
  let current = start;
  const api = async (path, init) => {
    assert.equal(path, "/v1/voice/tts");
    const body = JSON.parse(init.body);
    puts.push(body);
    if (refuse) {
      return json({ error: refuse }, 400);
    }
    const tts = { ...current.saved.tts, ...body };
    delete tts.confirm;
    delete tts.api_key;
    current = { ...current, saved: { tts }, restart_needed: true };
    return json(current);
  };
  return { api, puts };
}

function host(g, start, extra = {}) {
  const box = { info: start };
  const view = () =>
    h(SpeechService, {
      info: box.info,
      api: g.api,
      onInfo: (i) => {
        box.info = i;
      },
      ...extra,
    });
  return { box, view };
}

test("a gateway that cannot save, or an older one, shows nothing", async () => {
  const g = gateway(info(savedTts()));
  for (const i of [
    info(savedTts(), { can_save: false }),
    {
      enabled: false,
      wake_phrases: [],
      max_audio_bytes: 1,
      can_save: true,
      saved: {},
    },
  ]) {
    const m = await mount(
      h(SpeechService, { info: i, api: g.api, onInfo() {} }),
    );
    assert.equal(text(m.root), "");
    await m.done();
  }
});

test("turning the voice on with Qwen3-TTS: where the text goes, then a yes", async () => {
  const g = gateway(info(savedTts()));
  const hs = host(g, info(savedTts()));
  const m = await mount(hs.view());
  const rerender = () => m.set(hs.view());
  assert.match(
    text(m.root),
    /Off\. Turn it on to read answers aloud with a voice service\./,
  );
  assert.equal(m.tid("speech-service-switch").checked, false);
  assert.equal(m.tid("speech-service-confirm"), null);

  // Off while the form is open and nothing is saved just closes it.
  await change(m.tid("speech-service-switch"), true);
  assert.match(
    text(m.tid("speech-service-confirm")),
    /Turn on the voice service\?/,
  );
  assert.match(
    text(m.tid("speech-service-confirm")),
    /sent to https:\/\/llm\.example\/v1\./,
  );
  await change(m.tid("speech-service-switch"), false);
  assert.equal(m.tid("speech-service-confirm"), null);
  assert.equal(g.puts.length, 0);

  // The Qwen preset fills the form, and the text will go to DashScope.
  await change(m.tid("speech-service-switch"), true);
  await click(m.tid("speech-preset-qwen"));
  assert.equal(m.tid("speech-model").value, "qwen3-tts-flash");
  assert.equal(m.tid("speech-voice").value, "Cherry");
  assert.match(
    text(m.tid("speech-service-confirm")),
    /sent to https:\/\/dashscope-intl\.aliyuncs\.com\/api\/v1\./,
  );
  assert.equal(
    m.tid("speech-api-key").placeholder,
    "Not needed for a local server",
  );
  await type(m.tid("speech-api-key"), " dk ");
  await click(m.tid("speech-confirm-yes"));
  assert.deepEqual(g.puts[0], {
    enabled: true,
    confirm: true,
    api: "dashscope",
    model: "qwen3-tts-flash",
    voice: "Cherry",
    api_key: "dk",
  });
  await rerender();
  assert.equal(
    m.tid("speech-service-confirm"),
    null,
    "the form closes once saved",
  );
  assert.ok(m.tid("speech-service-restart"));
});

test("the voice that is on: its endpoint, a change, a typed address, a refusal and off", async () => {
  const on = info(
    savedTts({
      enabled: true,
      has_key: true,
      api: "dashscope",
      model: "qwen3-tts-flash",
      voice: "Cherry",
    }),
  );
  const g = gateway(on, { refuse: "no key" });
  const hs = host(g, on);
  const m = await mount(hs.view());
  assert.match(
    text(m.root),
    /Texts go to https:\/\/llm\.example\/v1 \(qwen3-tts-flash\)\./,
  );
  assert.equal(text(m.tid("speech-service-change")), "Change");

  await click(m.tid("speech-service-change"));
  assert.match(
    text(m.tid("speech-service-confirm")),
    /Change the voice service\?/,
  );
  assert.equal(
    m.tid("speech-service-change"),
    null,
    "the row hides while editing",
  );
  assert.equal(
    m.tid("speech-api-key").placeholder,
    "Saved. Paste a new one to replace it",
  );

  // An address typed, a blank model and voice that change nothing, and a refusal shown.
  await type(
    m.tid("speech-base-url"),
    " https://dashscope.aliyuncs.com/api/v1 ",
  );
  await type(m.tid("speech-model"), " ");
  await type(m.tid("speech-voice"), "");
  await click(m.tid("speech-confirm-yes"));
  assert.deepEqual(g.puts[0], {
    enabled: true,
    confirm: true,
    base_url: "https://dashscope.aliyuncs.com/api/v1",
  });
  assert.equal(m.root.querySelector("[role=alert]").textContent, "no key");
  assert.ok(m.tid("speech-service-confirm"), "a refused change keeps the form");

  // Back to OpenAI with nothing to inherit from: there is nowhere to send the text yet.
  await click(m.tid("speech-preset-openai"));
  await type(m.tid("speech-base-url"), "");
  assert.match(
    text(m.tid("speech-service-confirm")),
    /Name a speech service to read aloud with\./,
  );
  assert.equal(m.tid("speech-confirm-yes").disabled, true);

  // Cancel closes it; off needs no question.
  await click(
    [...m.root.querySelectorAll("button")].find((b) => text(b) === "Cancel"),
  );
  assert.equal(m.tid("speech-service-confirm"), null);
  const off = gateway(on);
  const hs2 = host(off, on);
  const m2 = await mount(hs2.view());
  await change(m2.tid("speech-service-switch"), false);
  assert.deepEqual(off.puts, [{ enabled: false }]);
  await m.done();
  await m2.done();
});

test("an openai form over a saved openai voice goes to the endpoint the file resolves", async () => {
  const g = gateway(info(savedTts({ enabled: true })));
  const m = await mount(
    h(SpeechService, {
      info: info(savedTts({ enabled: true })),
      api: g.api,
      onInfo() {},
    }),
  );
  await click(m.tid("speech-service-change"));
  assert.match(
    text(m.tid("speech-service-confirm")),
    /sent to https:\/\/llm\.example\/v1\./,
  );
  await m.done();
});

test("Read aloud shows the service only with its info and an API", async () => {
  const g = gateway(info(savedTts()));
  const base = {
    supported: true,
    prefs: {
      mode: "off",
      style: "literal",
      engine: "device",
      voice: "",
      rate: 1,
      volume: 1,
      pitch: 1,
      maxChars: 0,
      conversation: false,
      notices: false,
      subtitles: false,
      subtitleSize: "medium",
      byLanguage: false,
      langVoices: {},
      pronunciations: [],
    },
    setPrefs() {},
    voices: [],
    test() {},
  };
  const service = { info: info(savedTts()), onInfo() {} };
  const cases = [
    [{ api: g.api, service }, true],
    [{ api: g.api, service: { ...service, info: null } }, false],
    [{ service }, false],
    [{ api: g.api }, false],
  ];
  for (const [extra, shown] of cases) {
    const m = await mount(h(SpeechSettings, { ...base, ...extra }));
    assert.equal(!!m.tid("speech-service-switch"), shown);
    await m.done();
  }
});
