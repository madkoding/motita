// The "waiting for a new session" state, and the empty conversation's "Nothing yet" placeholder,
// must stay readable whatever part of the background image
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
// The colours of these cards are tokens now (themes.css); the contrast is pinned on the original
// dark Motita palette, the one in :root, and on the light ones by themes.test.mjs.
const root = /:root \{([^}]*)\}/.exec(src("themes.css"))[1];
const app = src("App.tsx");

const AA = 4.5;
// WCAG 3:1 minimum for non-text elements such as icons.
const NON_TEXT_AA = 3;
const BG = "background-color";

// The constants of the WCAG 2 relative luminance formula (sRGB).
const CHANNEL_MAX = 255;
const SRGB_LINEAR_LIMIT = 0.03928;
const SRGB_LINEAR_SLOPE = 12.92;
const SRGB_OFFSET = 0.055;
const SRGB_SCALE = 1.055;
const SRGB_GAMMA = 2.4;
const RED_WEIGHT = 0.2126;
const GREEN_WEIGHT = 0.7152;
const BLUE_WEIGHT = 0.0722;
// The flare WCAG adds to both luminances of a contrast ratio.
const FLARE = 0.05;
const PERCENT = 100;
// Where each channel's two hex digits start in "#rrggbb", and their base.
const HEX_DIGITS = 2;
const CHANNEL_OFFSETS = [0, 1, 2].map((c) => 1 + c * HEX_DIGITS);
const HEX = 16;
const RESTING_TINT = 0.1;
const HOVER_TINT = 0.2;

// rule returns the declarations of the first rule whose selector is exactly `selector`.
function rule(selector) {
  const at = css.indexOf(`\n${selector} {`);
  assert.ok(at >= 0, `${selector} is defined in index.css`);
  return css.slice(css.indexOf("{", at) + 1, css.indexOf("}", at));
}

function prop(body, name) {
  const decl = body
    .split(/[;\n]/)
    .map((d) => d.trim())
    .find((d) => d.startsWith(`${name}:`));
  const m = /:\s*(#[0-9a-fA-F]{6}|var\(--[\w-]+\))$/.exec(decl ?? "");
  assert.ok(m, `${name} is a solid #rrggbb colour or a token`);
  const token = /^var\((--[\w-]+)\)$/.exec(m[1]);
  if (!token) {
    return m[1];
  }
  const line = root
    .split("\n")
    .find((l) => l.trim().startsWith(`${token[1]}:`));
  const value = /(#[0-9a-fA-F]{6});/.exec(line ?? "");
  assert.ok(value, `${token[1]} is a solid colour in :root`);
  return value[1];
}

const rgb = (hex) =>
  CHANNEL_OFFSETS.map((i) => Number.parseInt(hex.slice(i, i + 2), HEX));

function luminance([r, g, b]) {
  const lin = (c) => {
    const s = c / CHANNEL_MAX;
    if (s <= SRGB_LINEAR_LIMIT) {
      return s / SRGB_LINEAR_SLOPE;
    }
    return ((s + SRGB_OFFSET) / SRGB_SCALE) ** SRGB_GAMMA;
  };
  return RED_WEIGHT * lin(r) + GREEN_WEIGHT * lin(g) + BLUE_WEIGHT * lin(b);
}

function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + FLARE) / (lo + FLARE);
}

// over blends `fg` at `alpha` over the opaque `bg`, as a translucent Tailwind tint renders.
const over = (fg, alpha, bg) =>
  fg.map((c, i) => Math.round(c * alpha + bg[i] * (1 - alpha)));

const card = rule(".no-session-card");
const cardBg = rgb(prop(card, BG));

test("the waiting state is drawn on the opaque card, not straight on the background image", () => {
  const body = app.slice(
    app.indexOf("function NoSession"),
    app.indexOf("function MainColumn"),
  );
  assert.match(body, /class="no-session-card/);
  assert.match(body, /class="no-session-title/);
  assert.match(body, /class="no-session-hint/);
  assert.doesNotMatch(
    body,
    /text-\[#9a9aaa\]/,
    "the old low-contrast grey is gone",
  );
});

test("the title clears WCAG AA on the card", () => {
  const ratio = contrast(rgb(prop(card, "color")), cardBg);
  assert.ok(ratio >= AA, `title contrast ${ratio.toFixed(2)}:1`);
});

test("the hint clears WCAG AA on the card", () => {
  const hint = rule(".no-session-card .no-session-hint");
  assert.equal(rgb(prop(hint, BG)).join(), cardBg.join());
  const ratio = contrast(rgb(prop(hint, "color")), cardBg);
  assert.ok(ratio >= AA, `hint contrast ${ratio.toFixed(2)}:1`);
});

test("the New session button text clears WCAG AA on its tint, resting and hovered", () => {
  const accent = rgb(/--color-accent:\s*(#[0-9a-fA-F]{6})/.exec(css)[1]);
  const button = src("NewSessionButton.ts");
  assert.match(
    button,
    /bg-accent\/10 [^"]*text-accent[^"]*hover:bg-accent\/20/,
  );
  for (const alpha of [RESTING_TINT, HOVER_TINT]) {
    const ratio = contrast(accent, over(accent, alpha, cardBg));
    assert.ok(
      ratio >= AA,
      `button contrast at ${alpha * PERCENT}% tint ${ratio.toFixed(2)}:1`,
    );
  }
});

test("the empty conversation placeholder is an empty state on an opaque card", () => {
  assert.match(app, /function isEmptyNote[\s\S]*?m\.text === NOTHING_YET/);
  assert.match(app, /choose\(isEmptyNote\(m\), " empty-note", ""\)/);
  const note = rule(".msg.kind.empty-note");
  assert.match(note, /text-transform:\s*none/, "no longer small capitals");
  const bg = rgb(prop(note, BG));
  const ratio = contrast(rgb(prop(note, "color")), bg);
  assert.ok(ratio >= AA, `placeholder contrast ${ratio.toFixed(2)}:1`);
  const icon = contrast(rgb(prop(rule(".empty-note-icon"), "color")), bg);
  assert.ok(
    icon >= NON_TEXT_AA,
    `icon contrast ${icon.toFixed(2)}:1 (non-text, 3:1)`,
  );
});

test("every other note sits on an opaque chip and clears WCAG AA", () => {
  const kind = rule(".msg.kind");
  assert.doesNotMatch(kind, /background:\s*transparent/);
  const ratio = contrast(rgb(prop(kind, "color")), rgb(prop(kind, BG)));
  assert.ok(ratio >= AA, `note contrast ${ratio.toFixed(2)}:1`);
});
