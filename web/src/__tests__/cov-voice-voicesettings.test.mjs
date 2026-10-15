// The voice section of the settings (VoiceSettings.tsx), loaded as the source itself: speech input
// on the gateway (turning it on, moving it, the wake phrases, a gateway that refuses), the
// microphone this device records with, and the browser's permission for it.
import test, { beforeEach } from "node:test";
import assert from "node:assert/strict";
import { setupDom, source, settle as settleWith, json } from "./cov-voice-helpers.mjs";

const dom = setupDom("https://localhost/");
const { VoiceSettings } = await source("VoiceSettings.tsx");
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
    q: (s) => root.querySelector(s),
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
  await act(() => input.dispatchEvent(new dom.window.Event("change", { bubbles: true })));
  await settle();
};
const type = async (input, value) => {
  input.value = value;
  await act(() => input.dispatchEvent(new dom.window.Event("input", { bubbles: true })));
  await settle();
};

// The browser's microphone: the Permissions API's answer, the devices, and getUserMedia.
const mic = { state: "granted", devices: [], enumFails: false, gum: undefined, listeners: new Set() };
function useMic({ permissions = true } = {}) {
  const md = {
    enumerateDevices: async () => {
      if (mic.enumFails) {
        throw new Error("no devices");
      }
      return mic.devices;
    },
    addEventListener: (_t, f) => mic.listeners.add(f),
    removeEventListener: (_t, f) => mic.listeners.delete(f),
  };
  if (mic.gum) {
    md.getUserMedia = mic.gum;
  }
  Object.defineProperty(navigator, "mediaDevices", { value: md, configurable: true });
  Object.defineProperty(navigator, "permissions", {
    value: permissions
      ? { query: async () => ({ state: mic.state, addEventListener() {}, removeEventListener() {} }) }
      : undefined,
    configurable: true,
  });
}
beforeEach(() => {
  mic.state = "granted";
  mic.devices = [{ kind: "audioinput", deviceId: "usb", label: "USB mic" }];
  mic.enumFails = false;
  mic.gum = undefined;
  useMic();
});

// These tests are about the transcription service: it is the one chosen to listen.
const prefs = { mic: true, listen: false, deviceId: "", engine: "service", listener: "", language: "" };
const savedVoice = (o = {}) => ({
  enabled: true,
  base_url: "",
  endpoint: "https://llm.example/v1",
  inherits: true,
  provider: "openai",
  model: "whisper-1",
  has_key: false,
  wake_phrases: ["hey motita"],
  ...o,
});
const voiceInfo = (saved, o = {}) => ({
  enabled: saved.enabled,
  wake_phrases: saved.wake_phrases ?? [],
  max_audio_bytes: 1,
  can_save: true,
  saved,
  ...o,
});

// A gateway that answers PUT /v1/voice with what it was given folded into saved, or refuses.
function gateway(start, { refuse = "" } = {}) {
  const puts = [];
  let info = start;
  const api = async (path, init) => {
    assert.equal(path, "/v1/voice");
    const change = JSON.parse(init.body);
    puts.push(change);
    if (refuse) {
      return json({ error: refuse }, 400);
    }
    const saved = { ...info.saved, ...change };
    delete saved.confirm;
    delete saved.api_key;
    info = { ...info, saved, enabled: saved.enabled, restart_needed: true };
    return json(info);
  };
  return { api, puts };
}

// A host that keeps the info and the prefs the section hands back.
function host(g, info, extra = {}) {
  const box = { info, prefs: { ...prefs } };
  const view = () =>
    h(VoiceSettings, {
      state: "ready",
      hands: "ready",
      phrases: ["hey motita"],
      prefs: box.prefs,
      setPrefs: (p) => {
        box.prefs = p;
      },
      info: box.info,
      api: g.api,
      onInfo: (i) => {
        box.info = i;
      },
      ...extra,
    });
  return { box, view };
}

test("speech input that is on: where it goes, a new service, the wake phrases, a restart to apply", async () => {
  const g = gateway(voiceInfo(savedVoice()));
  const hs = host(g, voiceInfo(savedVoice()));
  const m = await mount(hs.view());
  const rerender = () => m.set(hs.view());
  assert.match(text(m.root), /Recordings go to https:\/\/llm\.example\/v1 \(whisper-1\)\. With the language model's own key\./);
  assert.equal(text(m.tid("voice-service-change")), "Change");
  assert.equal(m.tid("voice-confirm"), null);

  // Change: the form opens with what is saved; a preset fills it; a key typed goes too.
  await click(m.tid("voice-service-change"));
  assert.match(text(m.tid("voice-confirm")), /Change the transcription service\?/);
  assert.equal(m.tid("voice-service-change"), null, "the service row hides while editing");
  assert.equal(m.tid("voice-api-key").placeholder, "Not needed for a local server");
  await type(m.tid("voice-base-url"), "https://stt.example/v1");
  assert.match(text(m.tid("voice-confirm")), /sent to https:\/\/stt\.example\/v1 to be turned into text/);
  await type(m.tid("voice-model"), "large-v3");
  await type(m.tid("voice-api-key"), " sk-1 ");
  await click(m.tid("voice-confirm-yes"));
  assert.deepEqual(g.puts.at(-1), {
    enabled: true,
    confirm: true,
    base_url: "https://stt.example/v1",
    model: "large-v3",
    api_key: "sk-1",
  });
  await rerender();
  assert.equal(m.tid("voice-confirm"), null, "saved: the form closes");
  assert.match(text(m.tid("voice-restart")), /Applies when the gateway restarts/);

  // The same service again: nothing but the switch is sent.
  await click(m.tid("voice-service-change"));
  await click(m.tid("voice-confirm-yes"));
  assert.deepEqual(g.puts.at(-1), { enabled: true, confirm: true });
  await rerender();

  // The wake phrases, saved once they differ.
  const save = m.tid("voice-phrases-save");
  assert.equal(save.disabled, true);
  await type(m.tid("voice-phrases"), "hey motita, ok computer");
  assert.equal(m.tid("voice-phrases-save").disabled, false);
  await click(m.tid("voice-phrases-save"));
  assert.deepEqual(g.puts.at(-1), {
    enabled: true,
    confirm: true,
    wake_phrases: ["hey motita", "ok computer"],
  });
  await rerender();
  assert.equal(m.tid("voice-phrases").value, "hey motita, ok computer");

  // Off: saved at once.
  await change(m.tid("voice-gateway-switch"), false);
  assert.deepEqual(g.puts.at(-1), { enabled: false });
  await rerender();
  assert.match(text(m.root), /Off\. Turn it on to dictate/);
  await m.done();
});

test("speech input that is off: the switch opens the form, and turning it back off closes it", async () => {
  const info = voiceInfo(savedVoice({ enabled: false, endpoint: "", inherits: false, has_key: true }));
  const g = gateway(info, { refuse: "no such host" });
  const hs = host(g, info);
  const m = await mount(hs.view());
  const sw = () => m.tid("voice-gateway-switch");
  await change(sw(), true);
  assert.equal(sw().checked, true, "on while the form is open");
  assert.match(text(m.tid("voice-confirm")), /Turn on speech input\?/);
  assert.match(text(m.tid("voice-confirm")), /This provider has no transcription service of its own/);
  assert.equal(m.tid("voice-confirm-yes").disabled, true, "nowhere to send it yet");
  assert.equal(m.tid("voice-base-url").placeholder, "https://api.openai.com/v1");
  assert.equal(m.tid("voice-api-key").placeholder, "Saved. Paste a new one to replace it");
  // Unticked before saying yes: the form closes, nothing is saved.
  await change(sw(), false);
  assert.equal(m.tid("voice-confirm"), null);
  assert.equal(g.puts.length, 0);

  // A preset, then a gateway that refuses: the form stays, with the reason.
  await change(sw(), true);
  await click([...m.tid("voice-confirm").querySelectorAll("button")].find((b) => text(b) === "OpenAI"));
  assert.equal(m.tid("voice-base-url").value, "https://api.openai.com/v1");
  assert.equal(text(m.tid("voice-confirm-yes")), "Turn on");
  await click(m.tid("voice-confirm-yes"));
  assert.equal(g.puts.at(-1).base_url, "https://api.openai.com/v1");
  assert.equal(text(m.q("[role=alert]")), "no such host");
  assert.ok(m.tid("voice-confirm"), "still asking");
  // Cancel closes it.
  await click([...m.tid("voice-confirm").querySelectorAll("button")].find((b) => text(b) === "Cancel"));
  assert.equal(m.tid("voice-confirm"), null);
  await m.done();
});

test("a saved voice with no wake phrases of its own shows none", async () => {
  const saved = savedVoice({ inherits: false });
  delete saved.wake_phrases;
  const g = gateway(voiceInfo(saved));
  const m = await mount(host(g, voiceInfo(saved)).view());
  assert.equal(m.tid("voice-phrases").value, "");
  assert.doesNotMatch(text(m.root), /language model's own key/);
  await m.done();
});

test("the microphone list: read again when devices change, one that went, a list that fails", async () => {
  const g = gateway(voiceInfo(savedVoice()));
  const hs = host(g, voiceInfo(savedVoice()));
  hs.box.prefs = { ...prefs, deviceId: "gone" };
  const m = await mount(hs.view());
  const sel = () => m.tid("mic-device");
  assert.deepEqual([...sel().options].map((o) => o.textContent), ["System default", "Not connected", "USB mic"]);
  assert.match(text(m.root), /The chosen microphone is not connected/);
  // Picking one hands the prefs back.
  sel().value = "usb";
  await act(() => sel().dispatchEvent(new dom.window.Event("change", { bubbles: true })));
  assert.equal(hs.box.prefs.deviceId, "usb");
  // The browser stops listing them: the list empties.
  mic.enumFails = true;
  await act(() => [...mic.listeners][0]());
  await settle();
  assert.deepEqual([...sel().options].map((o) => o.textContent), ["System default"]);
  assert.match(text(m.root), /Allow the microphone to see the names/);
  await m.done();
  assert.equal(mic.listeners.size, 0, "it stops listening when it goes");
});

test("the permission: asked, granted, refused, no microphone, and a page that went first", async () => {
  // Not asked yet: the row asks, and the rest waits for it.
  mic.state = "prompt";
  const tracks = [];
  let fail = null;
  mic.gum = async (c) => {
    assert.deepEqual(c, { audio: true });
    if (fail) {
      throw fail;
    }
    const tr = { stopped: false, stop() { this.stopped = true; } };
    tracks.push(tr);
    return { getTracks: () => [tr] };
  };
  useMic();
  const hs = host({ api: async () => json({}) }, null);
  const m = await mount(hs.view());
  assert.equal(m.tid("voice-locked").disabled, true);
  assert.match(text(m.tid("mic-permission")), /Allow the microphone to use voice/);

  // No microphone at all: said so.
  fail = Object.assign(new Error("none"), { name: "NotFoundError" });
  await click(m.tid("mic-allow"));
  assert.match(text(m.tid("mic-permission")), /No microphone was found on this device\./);
  // Refused.
  fail = Object.assign(new Error("no"), { name: "NotAllowedError" });
  await click(m.tid("mic-allow"));
  assert.equal(text(m.tid("mic-allow")), "Try again");
  assert.match(text(m.tid("mic-permission")), /was not allowed to use the microphone/);
  // Allowed: the tracks it opened are closed, and the settings unlock.
  fail = null;
  await click(m.tid("mic-allow"));
  assert.equal(tracks.length, 1);
  assert.equal(tracks[0].stopped, true);
  assert.equal(m.tid("mic-permission"), null);
  assert.equal(m.tid("voice-locked").disabled, false);
  await m.done();

  // A browser without getUserMedia: asking does nothing.
  mic.gum = undefined;
  useMic();
  const n = await mount(hs.view());
  await click(n.tid("mic-allow"));
  assert.ok(n.tid("mic-permission"), "still asking");
  await n.done();

  // Gone before the browser answered: the answer is dropped.
  let answer;
  Object.defineProperty(navigator, "permissions", {
    value: { query: () => new Promise((r) => (answer = r)) },
    configurable: true,
  });
  const root = document.createElement("div");
  await act(() => render(hs.view(), root));
  await act(() => render(null, root));
  answer({ state: "granted", addEventListener() {}, removeEventListener() {} });
  await settle();
  assert.equal(root.innerHTML, "", "nothing drawn after it went");
});

test("why a switch cannot be used: no recording, off or without phrases on the gateway, an insecure page", async () => {
  const view = (state, hands, info) =>
    h(VoiceSettings, {
      state,
      hands,
      phrases: [],
      prefs,
      setPrefs() {},
      info,
      api: info ? async () => json(info) : undefined,
      onInfo: info ? () => {} : undefined,
    });
  // This page cannot save: the configuration is where to change it.
  let m = await mount(view("off", "no-phrases", null));
  assert.match(text(m.root), /Off on the gateway: set voice\.enabled in the configuration\./);
  assert.match(text(m.root), /Add voice\.wake_phrases to the configuration first\./);
  await m.done();
  // This page can save: the section above is where.
  const info = voiceInfo(savedVoice({ enabled: false }));
  m = await mount(view("off", "no-phrases", info));
  assert.match(text(m.root), /Turn on speech input above first\./);
  assert.match(text(m.root), /Add a wake phrase above first\./);
  await m.done();
  // The browser listens, and has no model yet: the list above is where to get one.
  m = await mount(view("no-model", "no-model", null));
  assert.equal((text(m.root).match(/Download a model to listen with above first\./g) ?? []).length, 2);
  await m.done();
  // A browser that cannot record: nothing is asked of it.
  let asked = 0;
  Object.defineProperty(navigator, "permissions", {
    value: { query: async () => (asked++, { state: "granted" }) },
    configurable: true,
  });
  m = await mount(view("unsupported", "unsupported", null));
  assert.equal((text(m.root).match(/This browser cannot record audio\./g) ?? []).length, 2);
  await m.done();
  // An insecure page: the pill instead of a hint, under each switch.
  m = await mount(view("insecure", "insecure", null));
  assert.equal(m.root.querySelectorAll("[data-testid=insecure-warning]").length, 2);
  assert.equal(m.tid("mic-permission"), null);
  await m.done();
  assert.equal(asked, 0);
});

test("the permission changed in the browser's settings, on a browser with no device list", async () => {
  let status;
  Object.defineProperty(navigator, "permissions", {
    value: {
      query: async () => {
        status = {
          state: "denied",
          listeners: new Set(),
          addEventListener(_t, f) {
            this.listeners.add(f);
          },
          removeEventListener(_t, f) {
            this.listeners.delete(f);
          },
        };
        return status;
      },
    },
    configurable: true,
  });
  Object.defineProperty(navigator, "mediaDevices", { value: {}, configurable: true });
  const m = await mount(host({ api: async () => json({}) }, null).view());
  assert.equal(text(m.tid("mic-allow")), "Try again");
  status.state = "granted";
  await act(() => [...status.listeners][0]());
  await settle();
  assert.equal(m.tid("mic-permission"), null);
  assert.deepEqual([...m.tid("mic-device").options].map((o) => o.textContent), ["System default"]);
  await m.done();
  assert.equal(status.listeners.size, 0);
});
