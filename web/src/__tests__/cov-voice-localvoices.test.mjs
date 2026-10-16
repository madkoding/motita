// The local voices list (LocalVoiceSettings.tsx), loaded as the source itself, on what the
// catalog and the browser can throw at it: a quality it has no name for, a language code the
// browser cannot name, and a browser that cannot keep the list's filters.
import test from "node:test";
import assert from "node:assert/strict";
import {
  setupDom,
  source,
  settle as settleWith,
  json,
} from "./cov-voice-helpers.mjs";

const dom = setupDom("https://localhost/");
const lv = await source("LocalVoiceSettings.tsx");
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

const text = (el) => (el?.textContent ?? "").replace(/\s+/g, " ").trim();

test("a voice of a quality and a language the page has no name for shows them as they come", async () => {
  localStorage.setItem(
    lv.FILTERS_KEY,
    JSON.stringify({ language: "", quality: "" }),
  );
  const catalog = {
    runtime: { size: 9e6, installed: true },
    models: [
      {
        id: "odd",
        name: "Odd",
        lang: "x1",
        quality: "studio",
        engine: "piper",
        size: 2e6,
        installed: false,
        sample: false,
      },
      {
        id: "es",
        name: "Ana",
        lang: "es-MX",
        quality: "x-low",
        engine: "piper",
        size: 1e6,
        installed: false,
        sample: false,
      },
    ],
    download: null,
  };
  const api = async (path) => {
    assert.match(path, /^\/v1\/voice\/models/);
    return json(catalog);
  };
  const root = document.createElement("div");
  document.body.appendChild(root);
  await act(() =>
    render(h(lv.LocalVoices, { api, chosen: "", choose() {} }), root),
  );
  await settleWith(act, 4);
  const list = root.querySelector("[data-testid=local-voices]");
  assert.match(text(list), /Odd · x1 ?studio · 2 MB/);
  assert.match(text(list), /Ana · es-MX ?Very low quality · 1 MB/);
  const languages = [
    ...root.querySelectorAll("[data-testid=local-voice-language] option"),
  ].map((o) => o.textContent);
  assert.deepEqual(
    languages,
    ["Every language", "x1", "Spanish"],
    "the code where the browser has no name",
  );
  await act(() => render(null, root));
  root.remove();
});

test("filters a browser cannot keep are forgotten, without an error", () => {
  const ls = globalThis.localStorage;
  globalThis.localStorage = {
    getItem: () => null,
    setItem() {
      throw new dom.window.DOMException("full", "QuotaExceededError");
    },
  };
  try {
    assert.doesNotThrow(() =>
      lv.saveFilters({ language: "es", quality: "high" }),
    );
  } finally {
    globalThis.localStorage = ls;
  }
  lv.saveFilters({ language: "es", quality: "high" });
  assert.deepEqual(lv.loadFilters(), { language: "es", quality: "high" });
});
