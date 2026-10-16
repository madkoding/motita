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
const DQ = String.raw`"(?:\\.|[^"\\\n])*"`;
const SQ = String.raw`'(?:\\.|[^'\\\n])*'`;

// One master pattern per language; each alternative is a named group, so the loop
// below only has to read which one matched.
function pattern(r) {
  const comment = [];
  if (r.line) {
    comment.push(`${r.line.replaceAll("/", String.raw`\/`)}[^\n]*`);
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
    const text = esc(m[0]);
    out += cls ? `<span class="${cls}">${text}</span>` : text;
    last = m.index + m[0].length;
  }
  return out + esc(src.slice(last));
}

const LABELS = { js: "JavaScript", ts: "TypeScript", py: "Python", go: "Go", rust: "Rust", sh: "Shell", json: "JSON" };

// code renders one highlighted block, labelled with its language.
export function code(lang, src) {
  const label = LABELS[lang] ?? lang;
  return `<pre class="code" data-lang="${esc(label)}"><code>${highlight(lang, src.replace(/^\n|\n$/g, ""))}</code></pre>`;
}

// tabs renders the same example in several languages with no script: radio inputs
// pick the panel, so it works with JavaScript off and with the keyboard.
// items: [[lang, source], ...]; id must be unique on the page.
export function tabs(id, items) {
  const radios = items
    .map(([lang], i) => {
      const checked = i === 0 ? " checked" : "";
      return `<input type="radio" name="${id}" id="${id}-${lang}" aria-label="${esc(LABELS[lang] ?? lang)}"${checked} />`;
    })
    .join("");
  const bar = items.map(([lang]) => `<label for="${id}-${lang}">${esc(LABELS[lang] ?? lang)}</label>`).join("");
  const panels = items.map(([lang, src]) => `<section>${code(lang, src)}</section>`).join("");
  return `<div class="tabs">${radios}<div class="tab-bar">${bar}</div>${panels}</div>`;
}

// ---- diagrams --------------------------------------------------------------

let diagramCount = 0;

// flow renders a short chain of steps. It is HTML, not SVG, so the text wraps and
// scales with the page; the arrows turn from → to ↓ on a narrow screen.
// steps: [[title, detail?], ...]; note: one line under the chain.
export function flow(label, steps, note = "") {
  const items = steps
    .map(([title, detail], i) => {
      const sub = detail ? `<span>${detail}</span>` : "";
      return `<li><b>${i + 1}</b><strong>${title}</strong>${sub}</li>`;
    })
    .join("");
  const foot = note ? `<p class="flow-note">${note}</p>` : "";
  return `<figure class="diagram" role="group" aria-label="${esc(label)}"><ol class="flow">${items}</ol>${foot}</figure>`;
}

// Two arrowheads per diagram (accent and muted): context-stroke is not honoured
// everywhere, so each head carries its own colour class.
function defs(id) {
  const head = (suffix, cls) =>
    `<marker id="${id}${suffix}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path class="${cls}" d="M0 0L10 5L0 10z"/></marker>`;
  return `<defs>${head("", "dg-head")}${head("d", "dg-head dg-head-dash")}</defs>`;
}

const SEQ = { margin: 24, head: 40, top: 70, row: 50, noteH: 34, labelGap: 9 };

function seqLayout(actors, width) {
  const slot = (width - SEQ.margin * 2) / actors.length;
  return actors.map((a, i) => ({ ...a, x: SEQ.margin + slot * (i + 0.5), slot }));
}

function seqHead(a) {
  const w = Math.min(a.slot - 14, 170);
  const sub = a.sub ? `<text class="dg-sub" x="${a.x}" y="${SEQ.head - 8}" text-anchor="middle">${esc(a.sub)}</text>` : "";
  const title = a.sub ? 21 : 26;
  return `<rect class="dg-box" x="${a.x - w / 2}" y="6" width="${w}" height="${SEQ.head}" rx="8"/><text class="dg-title" x="${a.x}" y="${title}" text-anchor="middle">${esc(a.name)}</text>${sub}`;
}

function seqArrow(s, from, to, y, marker) {
  const dir = Math.sign(to.x - from.x) || 1;
  const x1 = from.x + dir * 3;
  const x2 = to.x - dir * 3;
  const cls = ["dg-arrow", s.dash ? "dg-dash" : "", s.tone ? `dg-${s.tone}` : ""].filter(Boolean).join(" ");
  const lines = Array.isArray(s.label) ? s.label : [s.label];
  const text = lines
    .map((l, k) => {
      const ly = y - SEQ.labelGap - (lines.length - 1 - k) * 15;
      return `<text class="dg-label" x="${(from.x + to.x) / 2}" y="${ly}" text-anchor="middle">${esc(l)}</text>`;
    })
    .join("");
  return `${text}<line class="${cls}" x1="${x1}" y1="${y}" x2="${x2}" y2="${y}" marker-end="url(#${marker}${s.dash ? "d" : ""})"/>`;
}

function seqNote(s, byName, y) {
  const xs = s.over.map((n) => byName[n].x);
  const centre = (Math.min(...xs) + Math.max(...xs)) / 2;
  const w = Math.max(Math.max(...xs) - Math.min(...xs) + 140, s.note.length * 8 + 32);
  return `<rect class="dg-note" x="${centre - w / 2}" y="${y - SEQ.noteH / 2}" width="${w}" height="${SEQ.noteH}" rx="6"/><text class="dg-label" x="${centre}" y="${y + 4}" text-anchor="middle">${esc(s.note)}</text>`;
}

// sequence renders a message-sequence diagram.
//   actors: [{name, sub?}]   steps: [{from, to, label, dash?, tone?} | {note, over: [name, ...]}]
export function sequence(label, actors, steps) {
  const width = Math.max(720, actors.length * 240);
  const placed = seqLayout(actors, width);
  const byName = Object.fromEntries(placed.map((a) => [a.name, a]));
  const height = SEQ.top + steps.length * SEQ.row + 6;
  const marker = `ah${++diagramCount}`;
  const lifelines = placed
    .map((a) => `<line class="dg-life" x1="${a.x}" y1="${SEQ.head + 6}" x2="${a.x}" y2="${height - 6}"/>`)
    .join("");
  const body = steps
    .map((s, i) => {
      const y = SEQ.top + i * SEQ.row + SEQ.row / 2 - 6;
      return s.note ? seqNote(s, byName, y) : seqArrow(s, byName[s.from], byName[s.to], y, marker);
    })
    .join("");
  return `<figure class="diagram" role="img" aria-label="${esc(label)}"><div class="dg-scroll"><svg viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" xmlns="http://www.w3.org/2000/svg">${defs(marker)}${lifelines}${placed.map(seqHead).join("")}${body}</svg></div></figure>`;
}

// hub renders clients on top, one gateway in the middle and the engine below it.
//   clients: [{name, sub}]   gateway: {name, sub}   engine: {name, sub}
export function hub(label, clients, gateway, engine) {
  const width = 560;
  const slot = (width - 24) / clients.length;
  const gw = { x: 90, y: 138, w: 380, h: 52 };
  const marker = `ah${++diagramCount}`;
  const leaves = clients
    .map((c, i) => {
      const cx = 12 + slot * (i + 0.5);
      const w = Math.min(slot - 10, 118);
      const tx = gw.x + gw.w * ((i + 0.5) / clients.length);
      return `<line class="dg-arrow" x1="${cx}" y1="64" x2="${tx}" y2="${gw.y - 2}" marker-end="url(#${marker})"/><rect class="dg-box" x="${cx - w / 2}" y="6" width="${w}" height="58" rx="8"/><text class="dg-title" x="${cx}" y="30" text-anchor="middle">${esc(c.name)}</text><text class="dg-sub" x="${cx}" y="48" text-anchor="middle">${esc(c.sub)}</text>`;
    })
    .join("");
  const box = (b, y, cls) =>
    `<rect class="${cls}" x="${gw.x}" y="${y}" width="${gw.w}" height="${gw.h}" rx="10"/><text class="dg-title" x="${width / 2}" y="${y + 22}" text-anchor="middle">${esc(b.name)}</text><text class="dg-sub" x="${width / 2}" y="${y + 40}" text-anchor="middle">${esc(b.sub)}</text>`;
  const down = `<line class="dg-arrow" x1="${width / 2}" y1="${gw.y + gw.h}" x2="${width / 2}" y2="236" marker-end="url(#${marker})"/>`;
  return `<figure class="diagram" role="img" aria-label="${esc(label)}"><div class="dg-scroll"><svg viewBox="0 0 ${width} 300" width="${width}" height="300" xmlns="http://www.w3.org/2000/svg">${defs(marker)}${leaves}${box(gateway, gw.y, "dg-box dg-hot")}${down}${box(engine, 238, "dg-box")}</svg></div></figure>`;
}
