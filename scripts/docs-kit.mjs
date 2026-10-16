// Building blocks for the Documentation pages (scripts/gen-docs.mjs, docs-es.mjs):
// syntax-highlighted code, CSS-only language tabs, and the diagrams.
//
// Everything is rendered at generation time into plain HTML and inline SVG, so the
// site stays static: no highlighter library, no script, nothing fetched. Colours
// come from the CSS variables in site/docs.css, so a diagram and a code block can
// never drift from the page they sit on.

const esc = (s) =>
  s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");

// ---- syntax highlighting ---------------------------------------------------

const words = (s) => new Set(s.split(/\s+/).filter(Boolean));

const JS_KEYWORDS =
  "async await break case catch class const continue default delete do else export extends finally for from function if import in instanceof let new of return static super switch this throw try typeof var void while yield true false null undefined";
const TS_EXTRA = "interface type enum implements readonly as private public protected abstract declare namespace keyof unknown never";

const RULES = {
  js: { words: words(JS_KEYWORDS), line: "//", block: true },
  ts: { words: words(`${JS_KEYWORDS} ${TS_EXTRA}`), line: "//", block: true },
  py: {
    words: words(
      "and as assert async await break class continue def del elif else except finally for from global if import in is lambda None nonlocal not or pass raise return try while with yield True False",
    ),
    line: "#",
    triple: true,
  },
  go: {
    words: words(
      "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var true false nil",
    ),
    line: "//",
    block: true,
    raw: true,
  },
  rust: {
    words: words(
      "as async await break const continue crate dyn else enum extern false fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait true type unsafe use where while",
    ),
    line: "//",
    block: true,
    rust: true,
  },
  json: { words: words("true false null") },
  sh: { line: "#", shell: true },
};

const NUMBER = String.raw`\b(?:0x[0-9a-fA-F_]+|\d[\d_]*(?:\.\d+)?(?:[eE][+-]?\d+)?)\b`;
const SLASH = String.raw`\/`;
const DQ = String.raw`"(?:\\.|[^"\\\n])*"`;
const SQ = String.raw`'(?:\\.|[^'\\\n])*'`;

// One master pattern per language; each alternative is a named group, so the loop
// below only has to read which one matched.
function pattern(r) {
  const comment = [];
  if (r.line) {
    comment.push(`${r.line.replaceAll("/", SLASH)}[^\n]*`);
  }
  if (r.block) {
    comment.push(String.raw`\/\*[\s\S]*?\*\/`);
  }
  const str = [];
  if (r.triple) {
    str.push(String.raw`"""[\s\S]*?"""`, String.raw`'''[\s\S]*?'''`);
  }
  str.push(DQ);
  if (r.rust) {
    // 'a is a lifetime, 'a' a char: only the second is a string.
    str.push(String.raw`'(?:\\.|[^'\\\n])'`);
  } else if (!r.json) {
    str.push(SQ);
  } else {
    // JSON has double-quoted strings only.
  }
  if (r.raw || r.line === "//") {
    str.push(String.raw`\x60(?:\\.|[^\x60\\])*\x60`);
  }
  const parts = [`(?<c>${comment.join("|") || "(?!)"})`, `(?<s>${str.join("|")})`, `(?<n>${NUMBER})`];
  if (r.shell) {
    parts.push(String.raw`(?<v>\$\{?\w+\}?)`, String.raw`(?<o>(?<![\w-])--?[A-Za-z][\w-]*)`);
  } else {
    parts.push(String.raw`(?<i>[A-Za-z_$][\w$]*)(?<call>\s*!?\s*\()?`);
  }
  return new RegExp(parts.join("|"), "g");
}

const compiled = {};
for (const [name, rule] of Object.entries(RULES)) {
  compiled[name] = pattern({ ...rule, json: name === "json" });
}

function identClass(rule, word, call) {
  if (rule.words?.has(word)) {
    return "k";
  }
  if (call) {
    return "f";
  }
  if (/^[A-Z]/.test(word)) {
    return "t";
  }
  return "";
}

function tokenClass(rule, g) {
  for (const key of ["c", "s", "n", "v", "o"]) {
    if (g[key] !== undefined) {
      return key;
    }
  }
  return identClass(rule, g.i, g.call);
}

function wrap(cls, text) {
  if (cls) {
    return `<span class="${cls}">${text}</span>`;
  }
  return text;
}

// when returns yes if the condition holds and no otherwise (a ternary, spelled out).
function when(cond, yes, no = "") {
  if (cond) {
    return yes;
  }
  return no;
}

// highlight turns source text into HTML with <span class="k|s|c|n|f|t|v|o"> tokens.
export function highlight(lang, src) {
  const rule = RULES[lang];
  if (!rule) {
    return esc(src);
  }
  let out = "";
  let last = 0;
  for (const m of src.matchAll(compiled[lang])) {
    const cls = tokenClass(rule, m.groups);
    out += esc(src.slice(last, m.index));
    out += wrap(cls, esc(m[0]));
    last = m.index + m[0].length;
  }
  return out + esc(src.slice(last));
}

const LABELS = { js: "JavaScript", ts: "TypeScript", py: "Python", go: "Go", rust: "Rust", sh: "Shell", json: "JSON" };
const label = (lang) => LABELS[lang] ?? lang;

// code renders one highlighted block, labelled with its language.
export function code(lang, src) {
  const body = highlight(lang, src.replace(/^\n|\n$/g, ""));
  return `<pre class="code" data-lang="${esc(label(lang))}"><code>${body}</code></pre>`;
}

// tabs renders the same example in several languages with no script: radio inputs
// pick the panel, so it works with JavaScript off and with the keyboard.
// items: [[lang, source], ...]; id must be unique on the page.
export function tabs(id, items) {
  const radios = items
    .map(([lang], i) => {
      const checked = when(i === 0, " checked");
      return `<input type="radio" name="${id}" id="${id}-${lang}" aria-label="${esc(label(lang))}"${checked} />`;
    })
    .join("");
  const bar = items.map(([lang]) => `<label for="${id}-${lang}">${esc(label(lang))}</label>`).join("");
  const panels = items.map(([lang, src]) => `<section>${code(lang, src)}</section>`).join("");
  return `<div class="tabs">${radios}<div class="tab-bar">${bar}</div>${panels}</div>`;
}

// ---- diagrams --------------------------------------------------------------

let diagramCount = 0;

function nextId() {
  diagramCount += 1;
  return `ah${diagramCount}`;
}

// Two arrowheads per diagram (accent and muted): context-stroke is not honoured
// everywhere, so each head carries its own colour class.
function defs(id) {
  const head = (suffix, cls) =>
    `<marker id="${id}${suffix}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path class="${cls}" d="M0 0L10 5L0 10z"/></marker>`;
  return `<defs>${head("", "dg-head")}${head("d", "dg-head dg-head-dash")}</defs>`;
}

function svg(name, width, height, inner) {
  return `<figure class="diagram"><div class="dg-scroll"><svg role="img" aria-label="${esc(name)}" viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" xmlns="http://www.w3.org/2000/svg">${inner}</svg></div></figure>`;
}

// flow renders a short chain of steps. It is HTML, not SVG, so the text wraps and
// scales with the page; the arrows turn from → to ↓ on a narrow screen.
// steps: [[title, detail?], ...]; note: one line under the chain.
export function flow(name, steps, note = "") {
  const items = steps
    .map(([title, detail], i) => {
      const sub = when(detail, `<span>${detail}</span>`);
      return `<li><b>${i + 1}</b><strong>${title}</strong>${sub}</li>`;
    })
    .join("");
  const foot = when(note, `<p class="flow-note">${note}</p>`);
  return `<figure class="diagram"><ol class="flow" aria-label="${esc(name)}">${items}</ol>${foot}</figure>`;
}

// Geometry of the sequence diagram, in SVG units.
const SEQ = {
  margin: 24,
  headH: 40,
  headY: 6,
  top: 70,
  row: 50,
  lift: 6,
  noteH: 34,
  labelGap: 9,
  lineH: 15,
  boxGap: 14,
  boxMax: 170,
  titleY: 26,
  titleYSub: 21,
  subY: 32,
  lifeGap: 6,
  tail: 6,
  pad: 3,
  textDy: 4,
  noteSpan: 140,
  charW: 8,
  notePad: 32,
  minWidth: 720,
  perActor: 240,
};

function seqHead(a) {
  const w = Math.min(a.slot - SEQ.boxGap, SEQ.boxMax);
  const title = when(a.sub, SEQ.titleYSub, SEQ.titleY);
  const sub = when(a.sub, `<text class="dg-sub" x="${a.x}" y="${SEQ.subY}" text-anchor="middle">${esc(a.sub ?? "")}</text>`);
  return `<rect class="dg-box" x="${a.x - w / 2}" y="${SEQ.headY}" width="${w}" height="${SEQ.headH}" rx="8"/><text class="dg-title" x="${a.x}" y="${title}" text-anchor="middle">${esc(a.name)}</text>${sub}`;
}

function seqLabel(lines, x, y) {
  return lines
    .map((l, k) => {
      const ly = y - SEQ.labelGap - (lines.length - 1 - k) * SEQ.lineH;
      return `<text class="dg-label" x="${x}" y="${ly}" text-anchor="middle">${esc(l)}</text>`;
    })
    .join("");
}

function seqArrow(s, from, to, y, marker) {
  const dir = Math.sign(to.x - from.x) || 1;
  const cls = ["dg-arrow", when(s.dash, "dg-dash")].filter(Boolean).join(" ");
  const head = `${marker}${when(s.dash, "d")}`;
  const lines = [s.label].flat();
  const text = seqLabel(lines, (from.x + to.x) / 2, y);
  return `${text}<line class="${cls}" x1="${from.x + dir * SEQ.pad}" y1="${y}" x2="${to.x - dir * SEQ.pad}" y2="${y}" marker-end="url(#${head})"/>`;
}

function seqNote(s, byName, y) {
  const xs = s.over.map((n) => byName[n].x);
  const centre = (Math.min(...xs) + Math.max(...xs)) / 2;
  const w = Math.max(Math.max(...xs) - Math.min(...xs) + SEQ.noteSpan, s.note.length * SEQ.charW + SEQ.notePad);
  return `<rect class="dg-note" x="${centre - w / 2}" y="${y - SEQ.noteH / 2}" width="${w}" height="${SEQ.noteH}" rx="6"/><text class="dg-label" x="${centre}" y="${y + SEQ.textDy}" text-anchor="middle">${esc(s.note)}</text>`;
}

function seqStep(s, byName, i, marker) {
  const y = SEQ.top + i * SEQ.row + SEQ.row / 2 - SEQ.lift;
  if (s.note) {
    return seqNote(s, byName, y);
  }
  return seqArrow(s, byName[s.from], byName[s.to], y, marker);
}

// sequence renders a message-sequence diagram.
//   actors: [{name, sub?}]   steps: [{from, to, label, dash?} | {note, over: [name, ...]}]
export function sequence(name, actors, steps) {
  const width = Math.max(SEQ.minWidth, actors.length * SEQ.perActor);
  const slot = (width - SEQ.margin * 2) / actors.length;
  const placed = actors.map((a, i) => ({ ...a, slot, x: SEQ.margin + slot * (i + 0.5) }));
  const byName = Object.fromEntries(placed.map((a) => [a.name, a]));
  const height = SEQ.top + steps.length * SEQ.row + SEQ.tail;
  const marker = nextId();
  const life = (a) =>
    `<line class="dg-life" x1="${a.x}" y1="${SEQ.headH + SEQ.lifeGap}" x2="${a.x}" y2="${height - SEQ.tail}"/>`;
  const body = steps.map((s, i) => seqStep(s, byName, i, marker)).join("");
  return svg(name, width, height, `${defs(marker)}${placed.map(life).join("")}${placed.map(seqHead).join("")}${body}`);
}

// Geometry of the hub diagram.
const HUB = {
  width: 560,
  height: 300,
  pad: 12,
  leafMax: 118,
  leafGap: 10,
  leafY: 6,
  leafH: 58,
  leafTitleY: 30,
  leafSubY: 48,
  gw: { x: 90, y: 138, w: 380, h: 52 },
  engineY: 238,
  downEnd: 236,
  titleOff: 22,
  subOff: 40,
  gap: 2,
};

function hubLeaf(c, i, count, marker) {
  const slot = (HUB.width - HUB.pad * 2) / count;
  const cx = HUB.pad + slot * (i + 0.5);
  const w = Math.min(slot - HUB.leafGap, HUB.leafMax);
  const tx = HUB.gw.x + HUB.gw.w * ((i + 0.5) / count);
  const start = HUB.leafY + HUB.leafH;
  return `<line class="dg-arrow" x1="${cx}" y1="${start}" x2="${tx}" y2="${HUB.gw.y - HUB.gap}" marker-end="url(#${marker})"/><rect class="dg-box" x="${cx - w / 2}" y="${HUB.leafY}" width="${w}" height="${HUB.leafH}" rx="8"/><text class="dg-title" x="${cx}" y="${HUB.leafTitleY}" text-anchor="middle">${esc(c.name)}</text><text class="dg-sub" x="${cx}" y="${HUB.leafSubY}" text-anchor="middle">${esc(c.sub)}</text>`;
}

function hubBox(b, y, cls) {
  const mid = HUB.width / 2;
  return `<rect class="${cls}" x="${HUB.gw.x}" y="${y}" width="${HUB.gw.w}" height="${HUB.gw.h}" rx="10"/><text class="dg-title" x="${mid}" y="${y + HUB.titleOff}" text-anchor="middle">${esc(b.name)}</text><text class="dg-sub" x="${mid}" y="${y + HUB.subOff}" text-anchor="middle">${esc(b.sub)}</text>`;
}

// hub renders clients on top, one gateway in the middle and the engine below it.
//   clients: [{name, sub}]   gateway: {name, sub}   engine: {name, sub}
export function hub(name, clients, gateway, engine) {
  const marker = nextId();
  const mid = HUB.width / 2;
  const leaves = clients.map((c, i) => hubLeaf(c, i, clients.length, marker)).join("");
  const down = `<line class="dg-arrow" x1="${mid}" y1="${HUB.gw.y + HUB.gw.h}" x2="${mid}" y2="${HUB.downEnd}" marker-end="url(#${marker})"/>`;
  const inner = `${defs(marker)}${leaves}${hubBox(gateway, HUB.gw.y, "dg-box dg-hot")}${down}${hubBox(engine, HUB.engineY, "dg-box")}`;
  return svg(name, HUB.width, HUB.height, inner);
}
