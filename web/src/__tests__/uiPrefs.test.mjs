import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html></html>", {
  url: "http://localhost/",
});
globalThis.document = dom.window.document;
globalThis.localStorage = dom.window.localStorage;
const m = await import("../uiPrefs.ts");

test("settings are applied and persisted", () => {
  assert.deepEqual(m.loadSettings(), m.DEFAULT_SETTINGS);
  m.saveSettings({ fontSize: "small", density: "compact" });
  assert.equal(document.documentElement.dataset.fontSize, "small");
  assert.equal(document.documentElement.dataset.density, "compact");
  assert.deepEqual(JSON.parse(localStorage.getItem(m.SETTINGS_KEY)), {
    fontSize: "small",
    density: "compact",
  });
  delete document.documentElement.dataset.density;
  m.initSettings();
  assert.equal(document.documentElement.dataset.density, "compact");
});

test("notifications: on by default, the switch is kept, and the state says what to show", () => {
  localStorage.clear();
  assert.equal(m.loadNotify(), true);
  m.saveNotify(false);
  assert.equal(m.loadNotify(), false);
  m.saveNotify(true);
  assert.equal(m.loadNotify(), true);
  assert.equal(m.notifyState(undefined, true), "unsupported");
  assert.equal(m.notifyState("denied", true), "blocked");
  assert.equal(m.notifyState("default", true), "ask");
  assert.equal(m.notifyState("granted", true), "on");
  assert.equal(m.notifyState("granted", false), "off");
});

test("the CI toast is on by default and its switch is kept apart from desktop notifications", () => {
  localStorage.clear();
  assert.equal(m.loadCiToast(), true);
  m.saveCiToast(false);
  assert.equal(m.loadCiToast(), false);
  assert.equal(
    m.loadNotify(),
    true,
    "desktop notifications are a separate switch",
  );
  m.saveCiToast(true);
  assert.equal(m.loadCiToast(), true);
});

test("notifications over plain http say https is needed, whatever the browser reports", () => {
  // Chrome reports "denied" on an insecure page, where no browser setting can allow them.
  assert.equal(m.notifyState("denied", true, false), "insecure");
  assert.equal(m.notifyState("default", true, false), "insecure");
  assert.equal(m.notifyState(undefined, true, false), "insecure");
  assert.equal(m.notifyState("granted", true, false), "on");
  assert.equal(m.notifyState("denied", true, true), "blocked");
});

test("the platform is read from the user agent, for the steps that unblock notifications", () => {
  const ua = {
    iphone:
      "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1",
    ipadDesktop:
      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15",
    android:
      "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36",
    edge: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36 Edg/129.0.0.0",
    chrome:
      "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
    firefox:
      "Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
  };
  assert.equal(m.notifyPlatform(ua.iphone), "ios");
  const ipadTouchPoints = 5;
  assert.equal(
    m.notifyPlatform(ua.ipadDesktop, ipadTouchPoints),
    "ios",
    "an iPad asking for the desktop site",
  );
  assert.equal(m.notifyPlatform(ua.ipadDesktop, 0), "safari", "a Mac");
  assert.equal(m.notifyPlatform(ua.android), "android");
  assert.equal(m.notifyPlatform(ua.edge), "edge");
  assert.equal(m.notifyPlatform(ua.chrome), "chrome");
  assert.equal(m.notifyPlatform(ua.firefox), "firefox");
  assert.equal(m.notifyPlatform("curl/8"), "other");
});
