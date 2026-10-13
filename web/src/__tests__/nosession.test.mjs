// The "waiting for a new session" state must stay readable whatever part of the background image
// is behind it: its words sit on an opaque card, and every colour on it clears WCAG AA (4.5:1).
// Run with: node --test web/src/__tests__/
import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const src = (f) => readFileSync(join(here, "..", f), "utf8");
const css = src("index.css");
const app = src("App.tsx");

const AA = 4.5;

// rule returns the declarations of the first rule whose selector is exactly `selector`.
function rule(selector) {
  const at = css.indexOf(`\n${selector} {`);
  assert.ok(at >= 0, `${selector} is defined in index.css`);
  return css.slice(css.indexOf("{", at) + 1, css.indexOf("}", at));
}

function prop(body, name) {
  const m = body.match(new RegExp(`(?:^|[;\\s])${name}:\\s*(#[0-9a-fA-F]{6})\\s*;`));
  assert.ok(m, `${name} is a solid #rrggbb colour`);
  return m[1];
}

const rgb = (hex) => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16));

function luminance([r, g, b]) {
  const lin = (c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

// over blends `fg` at `alpha` over the opaque `bg`, as a translucent Tailwind tint renders.
const over = (fg, alpha, bg) => fg.map((c, i) => Math.round(c * alpha + bg[i] * (1 - alpha)));

const card = rule(".no-session-card");
const cardBg = rgb(prop(card, "background-color"));

test("the waiting state is drawn on the opaque card, not straight on the background image", () => {
  const body = app.slice(app.indexOf("function NoSession"), app.indexOf("function MainColumn"));
  assert.match(body, /class="no-session-card/);
  assert.match(body, /class="no-session-title/);
  assert.match(body, /class="no-session-hint/);
  assert.doesNotMatch(body, /text-\[#9a9aaa\]/, "the old low-contrast grey is gone");
});

test("the title clears WCAG AA on the card", () => {
  const ratio = contrast(rgb(prop(card, "color")), cardBg);
  assert.ok(ratio >= AA, `title contrast ${ratio.toFixed(2)}:1`);
});

test("the hint clears WCAG AA on the card", () => {
  const hint = rule(".no-session-card .no-session-hint");
  assert.equal(rgb(prop(hint, "background-color")).join(), cardBg.join());
  const ratio = contrast(rgb(prop(hint, "color")), cardBg);
  assert.ok(ratio >= AA, `hint contrast ${ratio.toFixed(2)}:1`);
});

test("the New session button text clears WCAG AA on its tint, resting and hovered", () => {
  const accent = rgb(css.match(/--color-accent:\s*(#[0-9a-fA-F]{6})/)[1]);
  const button = src("NewSessionButton.ts");
  assert.match(button, /bg-accent\/10 [^"]*text-accent[^"]*hover:bg-accent\/20/);
  for (const alpha of [0.1, 0.2]) {
    const ratio = contrast(accent, over(accent, alpha, cardBg));
    assert.ok(ratio >= AA, `button contrast at ${alpha * 100}% tint ${ratio.toFixed(2)}:1`);
  }
});
