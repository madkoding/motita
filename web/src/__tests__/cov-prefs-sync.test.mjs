// The page's preferences on the gateway: what is sent and what stays on the device, the one-time
// migration of what a browser saved before, the gateway's value winning, and the hook that does it.
import test from "node:test";
import assert from "node:assert/strict";
import {
  installDom,
  source,
  flush,
  renderHook,
} from "./cov-stream-helpers.mjs";

installDom();
const prefs = await source("serverPrefs.ts");
const { usePrefsSync } = await source("usePrefsSync.ts");
const theme = await source("theme.ts");
const ui = await source("uiPrefs.ts");

const THEME = "motita.ui.theme";
const SPEECH = "motita.ui.speech";
const VOICE = "motita.ui.voice";
const reply = (prefs) => ({ ok: true, json: async () => ({ prefs }) });

// gateway answers like /v1/ui/prefs and records what is PUT.
function gateway(held, { ok = true } = {}) {
  const puts = [];
  const fn = async (path, init) => {
    assert.equal(path, prefs.PREFS_PATH);
    if (init?.method === "PUT") {
      const sent = JSON.parse(init.body).prefs;
      puts.push(sent);
      Object.assign(held, sent);
      return reply(held);
    }
    if (!ok) {
      return { ok: false };
    }
    return reply(held);
  };
  return { fn, puts };
}

test("a value is sent without its device fields", () => {
  assert.equal(prefs.shared(THEME, '{"mode":"dark"}'), '{"mode":"dark"}');
  assert.equal(prefs.shared("motita.ui.nextHint", "off"), "off");
  assert.equal(
    prefs.shared(VOICE, '{"deviceId":"mic1","mic":true,"language":"es"}'),
    '{"language":"es"}',
  );
  assert.equal(prefs.shared(VOICE, "not json"), "not json");
  assert.equal(prefs.shared(VOICE, "[1]"), "[1]");
});

test("the gateway's value is laid over the browser's, which keeps its device fields", () => {
  assert.equal(
    prefs.merged(THEME, '{"mode":"light"}', '{"mode":"dark"}'),
    '{"mode":"light"}',
  );
  assert.equal(prefs.merged("motita.ui.nextHint", "on", "off"), "on");
  const merged = JSON.parse(
    prefs.merged(
      VOICE,
      '{"language":"es","mic":false}',
      '{"deviceId":"mic1","mic":true,"language":"en"}',
    ),
  );
  assert.deepEqual(merged, { language: "es", mic: true, deviceId: "mic1" });
  assert.equal(
    prefs.merged(VOICE, '{"language":"es"}', null),
    '{"language":"es"}',
  );
  assert.equal(prefs.merged(VOICE, "oops", "{}"), "oops");
});

test("syncing: the gateway wins, what only the browser has goes up once", async () => {
  localStorage.clear();
  localStorage.setItem(THEME, '{"mode":"dark","scheme":"violet"}');
  localStorage.setItem("motita.ui.nextHint", "off");
  localStorage.setItem(
    VOICE,
    '{"deviceId":"mic1","language":"en","sendAfter":3}',
  );
  const held = { [THEME]: '{"mode":"light","scheme":"rose"}' };
  const g = gateway(held);

  assert.equal(await prefs.syncPrefs(g.fn), true);
  assert.equal(localStorage.getItem(THEME), '{"mode":"light","scheme":"rose"}');
  assert.equal(held["motita.ui.nextHint"], "off", "migrated");
  assert.deepEqual(JSON.parse(held[VOICE]), { language: "en", sendAfter: 3 });
  assert.equal(g.puts.length, 1);
  assert.equal(
    JSON.parse(localStorage.getItem(VOICE)).deviceId,
    "mic1",
    "the device field stays",
  );

  // Nothing new: no change, nothing sent.
  assert.equal(await prefs.syncPrefs(g.fn), false);
  assert.equal(g.puts.length, 1);
});

test("syncing answers false when the gateway has no preferences or cannot be read", async () => {
  localStorage.clear();
  assert.equal(await prefs.syncPrefs(gateway({}, { ok: false }).fn), false);
  const empty = { ok: true, json: async () => ({}) };
  assert.equal(await prefs.syncPrefs(async () => empty), false);
});

test("syncing keeps what the browser had when storage refuses the write", async () => {
  localStorage.clear();
  const real = window.Storage.prototype.setItem;
  window.Storage.prototype.setItem = () => {
    throw new Error("full");
  };
  try {
    assert.equal(
      await prefs.syncPrefs(gateway({ [THEME]: '{"mode":"light"}' }).fn),
      false,
    );
  } finally {
    window.Storage.prototype.setItem = real;
  }
});

test("a save is sent once, after a moment, and only once the page is bound", async () => {
  localStorage.clear();
  const g = gateway({});
  prefs.bindPrefs(null);
  ui.saveNotify(false);
  prefs.flushPrefs();
  assert.equal(g.puts.length, 0, "not bound: nothing is sent");

  prefs.bindPrefs(g.fn);
  ui.saveNotify(false);
  ui.saveNotify(true);
  theme.saveTheme({ mode: "dark", scheme: "slate" });
  prefs.pushPref("motita.ui.other");
  await new Promise((r) => setTimeout(r, prefs.PUSH_DELAY_MS + 50));
  assert.equal(g.puts.length, 1);
  assert.deepEqual(g.puts[0], {
    "motita.ui.notifications": "on",
    [THEME]: '{"mode":"dark","scheme":"slate"}',
  });

  // A key whose value was removed is skipped; a failing gateway is silent.
  prefs.bindPrefs(async () => {
    throw new Error("down");
  });
  localStorage.removeItem(THEME);
  prefs.pushPref(THEME);
  prefs.pushPref(SPEECH);
  localStorage.setItem(SPEECH, "{}");
  prefs.flushPrefs();
  await flush();
  prefs.bindPrefs(null);
});

test("pushing reads nothing back when storage is blocked", () => {
  prefs.bindPrefs(async () => reply({}));
  const real = window.Storage.prototype.getItem;
  window.Storage.prototype.getItem = () => {
    throw new Error("blocked");
  };
  try {
    assert.doesNotThrow(() => {
      prefs.pushPref(THEME);
      prefs.flushPrefs();
    });
  } finally {
    window.Storage.prototype.getItem = real;
    prefs.bindPrefs(null);
  }
});

test("the hook reads the gateway once signed in and applies what changed", async () => {
  localStorage.clear();
  localStorage.setItem(THEME, '{"mode":"dark","scheme":"motita"}');
  const held = {
    [THEME]: '{"mode":"light","scheme":"rose"}',
    "motita.ui.settings": '{"fontSize":"large"}',
  };
  const g = gateway(held);
  const seen = [];
  for (const n of ["motita-voice-prefs", "motita-usage-style"]) {
    window.addEventListener(n, () => seen.push(n));
  }
  const r = await renderHook((p) => usePrefsSync(g.fn, p.auth), {
    auth: "needed",
  });
  await flush();
  assert.equal(
    localStorage.getItem(THEME),
    '{"mode":"dark","scheme":"motita"}',
  );

  await r.rerender({ auth: "ok" });
  await flush();
  assert.equal(document.documentElement.dataset.scheme, "rose");
  assert.equal(document.documentElement.dataset.fontSize, "large");
  assert.deepEqual(seen.sort(), ["motita-usage-style", "motita-voice-prefs"]);

  // Another browser changed the theme: coming back into view picks it up, being hidden does not.
  held[THEME] = '{"mode":"dark","scheme":"slate"}';
  const view = (state) => {
    Object.defineProperty(document, "visibilityState", {
      value: state,
      configurable: true,
    });
    document.dispatchEvent(new window.Event("visibilitychange"));
  };
  view("hidden");
  await flush();
  assert.equal(document.documentElement.dataset.scheme, "rose");
  view("visible");
  await flush();
  assert.equal(document.documentElement.dataset.scheme, "slate");

  // Signing out unbinds: a save no longer goes up.
  await r.unmount();
  ui.saveNotify(false);
  prefs.flushPrefs();
  assert.equal(
    g.puts.some((sent) => "motita.ui.notifications" in sent),
    false,
  );
});

test("the hook leaves the page alone when nothing changed, was unmounted, or the gateway fails", async () => {
  localStorage.clear();
  const held = {};
  const calm = await renderHook((p) => usePrefsSync(gateway(held).fn, p), "ok");
  await flush();
  await calm.unmount();

  const slow = gateway({ [THEME]: '{"mode":"light"}' });
  const early = await renderHook((p) => usePrefsSync(slow.fn, p), "ok");
  await early.unmount();
  await flush();

  const broken = await renderHook(
    (p) =>
      usePrefsSync(async () => {
        throw new Error("older gateway");
      }, p),
    "ok",
  );
  await flush();
  await broken.unmount();
});

test("syncing waits while a save is still on its way", async () => {
  localStorage.clear();
  const g = gateway({ [THEME]: '{"mode":"light"}' });
  prefs.bindPrefs(g.fn);
  ui.saveNotify(false);
  assert.equal(await prefs.syncPrefs(g.fn), false);
  assert.equal(localStorage.getItem(THEME), null);
  prefs.flushPrefs();
  await flush();
  assert.equal(await prefs.syncPrefs(g.fn), true);
  prefs.bindPrefs(null);
});
