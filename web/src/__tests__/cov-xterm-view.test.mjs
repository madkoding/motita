// The interactive terminal's screen (xtermView.ts) on a stand-in xterm.js: the options it is
// opened with, the fit that follows its box, and what each method hands to the emulator.
// Run with: node --test web/src/__tests__/cov-xterm-view.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, stub } from "./cov-shell-helpers.mjs";

setupDom();

const made = { terms: [], fits: [], observers: [] };
globalThis.__xtermFake = made;
stub(
  "@xterm/xterm",
  `export class Terminal {
  constructor(opts) { this.opts = opts; this.cols = 80; this.rows = 24; this.calls = []; globalThis.__xtermFake.terms.push(this); }
  loadAddon(a) { this.addon = a; }
  open(el) { this.host = el; }
  write(d) { this.calls.push(["write", d]); }
  onData(fn) { this.dataFn = fn; }
  onResize(fn) { this.resizeFn = fn; }
  focus() { this.calls.push(["focus"]); }
  dispose() { this.calls.push(["dispose"]); }
}`,
);
stub(
  "@xterm/addon-fit",
  `export class FitAddon {
  constructor() { this.n = 0; globalThis.__xtermFake.fits.push(this); }
  fit() { this.n++; }
}`,
);
stub("@xterm/xterm/css/xterm.css", "export {};");
globalThis.ResizeObserver = class {
  constructor(cb) {
    this.cb = cb;
    this.watched = [];
    made.observers.push(this);
  }
  observe(el) {
    this.watched.push(el);
  }
  disconnect() {
    this.off = true;
  }
};

const { createView } = await load("xtermView.ts");

test("the screen opens in its box in the drawer's colours and follows the box's size", () => {
  const host = document.createElement("div");
  host.style.setProperty("--tx-rgb", "1, 2, 3");
  host.style.setProperty("--font-mono", "Mono");
  document.body.appendChild(host);
  const v = createView(host);
  const term = made.terms.at(-1);
  const fit = made.fits.at(-1);
  const ro = made.observers.at(-1);
  assert.equal(term.host, host);
  assert.equal(term.addon, fit);
  assert.equal(term.opts.theme.foreground, "rgb(1, 2, 3)");
  assert.equal(term.opts.fontFamily, "Mono");
  assert.equal(term.opts.allowTransparency, true);
  assert.equal(fit.n, 1);
  assert.deepEqual(ro.watched, [host]);
  ro.cb();
  v.fit();
  assert.equal(fit.n, 3);

  assert.deepEqual(v.size(), { cols: 80, rows: 24 });
  const bytes = new Uint8Array([104, 105]);
  v.write(bytes);
  v.focus();
  const typed = [];
  v.onData((d) => typed.push(d));
  term.dataFn("ls");
  const sizes = [];
  v.onResize((c, r) => sizes.push([c, r]));
  term.resizeFn({ cols: 9, rows: 3 });
  assert.deepEqual(typed, ["ls"]);
  assert.deepEqual(sizes, [[9, 3]]);
  v.dispose();
  assert.ok(ro.off);
  assert.deepEqual(term.calls, [["write", bytes], ["focus"], ["dispose"]]);
});

test("a page without the drawer's tokens gets the TTY green and a monospace face", () => {
  createView(document.createElement("div"));
  const term = made.terms.at(-1);
  assert.equal(term.opts.theme.foreground, "rgb(126, 231, 135)");
  assert.equal(term.opts.fontFamily, "monospace");
});
