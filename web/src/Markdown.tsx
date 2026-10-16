// The Markdown the agent's answers are rendered with.
//
// This replaced a hand-written, dependency-free renderer that had grown to cover
// headings, emphasis, code, lists, tables, footnotes and alerts on its own. It was
// 423 lines of regex and it could not get the corner cases right: measured on a
// document that exercises the CommonMark spec plus the usual extensions, it left
// TWO TRAILING SPACES (a hard line break) as text, it turned `\*not italic\*` into
// an emphasis with a visible backslash, and it had no idea what math or a diagram
// was. Three things it claimed to support had never worked:
//
//   * tables with alignment (`|:---|:---:|` parsed as plain cells);
//   * autolinks (`<https://x>` stayed as escaped text);
//   * images, which were only allowed from `data:` URLs and dropped otherwise.
//
// So the parsing is now a real CommonMark implementation with the GFM extensions,
// and this file is the part that is actually ours: WHICH extensions, what the
// output is allowed to contain, and the three things the old renderer got right
// that must keep working (the copy button, the raw-text attribute it copies from,
// and running while an answer is still streaming).
//
// ## Math, code colours and diagrams are DEFERRED, and that is the design
//
// The parse produces a skeleton of inert placeholders; the three expensive halves
// are filled in afterwards, and each of their modules is imported only when a
// message actually contains one. This is not a micro-optimization: the page is
// embedded in a Go binary under a hard budget and those three are well over a
// megabyte of source between them. Measured, with them imported eagerly, the shell
// alone was 486 KB of JavaScript.
//
// ## Every payload travels as TEXT, never as an attribute — and that is load-bearing
//
// A placeholder has to carry the source it will be rendered from, and the obvious
// place is a `data-` attribute. DOMPurify removes exactly that one, and the reason is
// worth stating because it looks like a bug and is not:
//
//   `SAFE_FOR_XML` (on by default) drops any attribute whose VALUE contains `<!--`,
//   `-->` or `<![CDATA[` — an HTML comment marker inside an attribute value is how an
//   mXSS payload escapes a sanitizer. Measured against this build: `<span data-math=
//   "A --> B">` comes back as `<span>` with no attribute at all, while the same
//   string as TEXT CONTENT survives untouched.
//
// Every mermaid edge is written `-->`. So the first version of this file shipped a
// diagram placeholder whose source attribute had been silently deleted, mermaid
// received the empty string, and the reader got "no diagram type detected" instead of
// their flowchart. The same filter would have emptied the attribute of a formula or a
// code block containing `-->`, and of `data-raw` — which is the copy button's payload,
// so copying a JavaScript answer would have produced an empty clipboard.
//
// Disabling `SAFE_FOR_XML` to make the attributes stick was measured and rejected: it
// is a real weakening, not a formality. `<noscript><p title="</noscript><img src=x
// onerror=alert(1)>">` comes back with the handler intact in the title attribute.
//
// So the payloads live in text content, and `hydrate` puts the attributes back AFTER
// the sanitizer has run — `setAttribute` assigns a value, it does not parse HTML, so
// nothing is reintroduced. The parse and the copy button keep the contract they had.
//
// ## `html: true` is deliberate, and it is why nothing reaches the DOM unsanitized
//
// Inline HTML in an answer renders (`<kbd>`, `<sup>`, `<details>`), which the old
// renderer escaped. That widening is only safe because every fragment — the parsed
// message, a KaTeX render and a mermaid SVG alike — goes through DOMPurify first.
import { useEffect, useLayoutEffect, useMemo, useRef } from "preact/hooks";
import type { Config as PurifyConfig } from "dompurify";
import MarkdownIt from "markdown-it";
import type { MarkdownItOptions, Token } from "markdown-it";
import DOMPurify from "dompurify";
import { watchForHydration } from "./hydration";
import { streamReveal, type StreamReveal } from "./streamReveal";
import { t } from "./i18n";
import footnote from "markdown-it-footnote";
import deflist from "markdown-it-deflist";
import taskLists from "markdown-it-task-lists";
import alerts from "markdown-it-github-alerts";
import { choose } from "./choose";
import { errorMessage } from "./errors";

/** Escapes a string for use inside a double-quoted attribute. */
function escapeAttr(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

/**
 * The parser. Options are explicit rather than defaulted because two of them change
 * what a reader sees:
 *
 *   breaks: false  - CommonMark: one newline is NOT a line break, two trailing spaces
 *                    are. `breaks: true` would silently redefine every multi-line
 *                    answer, and the old renderer followed CommonMark here.
 *   linkify: false - `<https://x>` autolinks (CommonMark) work; bare `www.x` in prose
 *                    does not become a link, which is what the old renderer did and
 *                    the less surprising behaviour in a transcript.
 */
const md = new MarkdownIt({
  html: true,
  breaks: false,
  linkify: false,
  typographer: false,
})
  .use(footnote)
  .use(deflist)
  .use(taskLists, { label: true })
  .use(alerts);

/**
 * `$$ ... $$` as a display formula.
 *
 * markdown-it has no rule for this, so the block is taken here rather than left to
 * fall through to the paragraph rule — which is what happened before: the closing `$$`
 * and the formula underneath it came out as ordinary text inside a `<p>`.
 *
 * The rule runs at line START only. A `$$` that opens mid-sentence is left alone,
 * because a mid-line `$$` is far more likely to be money or an escaped pair than a
 * display formula, and guessing wrong would eat the reader's text.
 */
// scanMathBlock reads a `$$` block that spans lines, up to the line with the closing `$$`.
// The opening `$$` may have the formula on it, or nothing at all. It returns null when the
// block is never closed.
function scanMathBlock(
  state: { bMarks: number[]; tShift: number[]; eMarks: number[]; src: string },
  startLine: number,
  endLine: number,
  afterOpen: string,
): { content: string; nextLine: number } | null {
  const lines: string[] = choose(afterOpen.trim() === "", [], [afterOpen]);
  for (let line = startLine + 1; line < endLine; line++) {
    const ls = state.bMarks[line] + state.tShift[line];
    const le = state.eMarks[line];
    const text = state.src.slice(ls, le);
    const close = text.indexOf("$$");
    if (close >= 0) {
      const head = text.slice(0, close);
      if (head.trim() !== "") {
        lines.push(head);
      }
      return { content: lines.join("\n"), nextLine: line + 1 };
    }
    lines.push(text);
  }
  return null;
}

md.block.ruler.before(
  "fence",
  "math_block",
  (state, startLine, endLine, silent): boolean => {
    const start = state.bMarks[startLine] + state.tShift[startLine];
    const max = state.eMarks[startLine];
    if (state.src.slice(start, start + 2) !== "$$") {
      return false;
    }

    const afterOpen = state.src.slice(start + 2, max);
    let content: string;
    let nextLine: number;

    const sameLine = afterOpen.indexOf("$$");
    if (sameLine >= 0) {
      // `$$ x^2 $$` on one line.
      content = afterOpen.slice(0, sameLine);
      nextLine = startLine + 1;
    } else {
      const block = scanMathBlock(state, startLine, endLine, afterOpen);
      // An unclosed `$$` is not a formula: it stays as the reader wrote it.
      if (!block) {
        return false;
      }
      content = block.content;
      nextLine = block.nextLine;
    }

    if (silent) {
      return true;
    }
    const token = state.push("math_block", "math", 0);
    token.block = true;
    token.content = content.trim();
    token.map = [startLine, nextLine];
    state.line = nextLine;
    return true;
  },
  { alt: ["paragraph", "reference", "blockquote", "list"] },
);

// A fence is one of four things, and none of them can be finished here: a diagram
// and a formula need a module that may not be loaded yet, and code needs one that
// should not be loaded unless there is code.
//
// Each placeholder carries its source as TEXT (see the note at the top of the file)
// and only safe flags as attributes.
md.renderer.rules.fence = (tokens: Token[], idx: number): string => {
  const token = tokens[idx];
  const info = (token.info || "").trim().split(/\s+/)[0].toLowerCase();
  const raw = token.content.replace(/\n$/, "");

  if (info === "mermaid") {
    // Inert: `hydrate` fills it in. Whether the source is a valid diagram is decided
    // at RENDER time, never here.
    return (
      '<div class="mermaid-block">' + md.utils.escapeHtml(raw) + "</div>\n"
    );
  }
  if (info === "math") {
    return (
      '<div class="math-block" data-display="1">' +
      md.utils.escapeHtml(raw) +
      "</div>\n"
    );
  }

  // Code, emitted PLAIN and coloured later. The raw text is the <code> element's own
  // content, and `hydrate` copies it into the `data-copy-text` attribute the delegated
  // click handler in App.tsx reads.
  return (
    '<pre class="code-block" data-code-lang="' +
    escapeAttr(info) +
    // The copy button is added by sanitizeMessage AFTER the sanitizer: a message may not
    // carry a button of its own.
    '"><code' +
    choose(
      info,
      ' class="' + escapeAttr("lang-" + info) + ' hljs"',
      ' class="hljs"',
    ) +
    ">" +
    md.utils.escapeHtml(raw) +
    "</code></pre>\n"
  );
};

// `$$` handled at block level above; the renderer for it.
md.renderer.rules.math_block = (tokens: Token[], idx: number): string =>
  '<div class="math-block" data-display="1">' +
  md.utils.escapeHtml(tokens[idx].content) +
  "</div>\n";

/**
 * Inline math: `$...$`.
 *
 * Taken from the TEXT token, because markdown-it has no inline rule for it. The guards
 * follow pandoc's rule, and are what keep prose with prices in it intact:
 * - the opening `$` may not be followed by whitespace (`$ 5` is not a formula);
 * - the closing `$` may not follow whitespace, so in "$5 and $10" the text between the
 *   two dollars ("5 and ") cannot be a formula;
 * - the closing `$` may not be followed by a digit ("$5 and$10") or by another `$`, so
 *   `$$b$$` is not read as a formula between stray dollars.
 * A `$` that is never closed stays as it was written. A one-character formula (`$x$`)
 * is both its first and its last character.
 */
const INLINE_MATH = /\$(?!\s)([^$\n]*?[^\s$])\$(?![\d$])/g;
const MATH_OPEN = '<span class="math-inline">';
const MATH_CLOSE = "</span>";

// markdown-it ships a text rule of its own; it is kept for the text that holds no `$`.
const defaultText = md.renderer.rules.text as NonNullable<
  typeof md.renderer.rules.text
>;
md.renderer.rules.text = (
  tokens: Token[],
  idx: number,
  options: Required<MarkdownItOptions>,
  env,
  self,
): string => {
  const token = tokens[idx];
  if (!token.content.includes("$")) {
    return defaultText(tokens, idx, options, env, self);
  }

  // Escaped gap by gap, around the placeholders that are inserted verbatim. Walking
  // the matches explicitly is what keeps this honest: the alternative (replace, then
  // split on the inserted markup) has to reconstruct where the real HTML was, and a
  // formula whose TeX happens to contain the placeholder text escapes that.
  let out = "";
  let last = 0;
  for (const m of token.content.matchAll(INLINE_MATH)) {
    out += md.utils.escapeHtml(token.content.slice(last, m.index));
    out += MATH_OPEN + md.utils.escapeHtml(m[1]) + MATH_CLOSE;
    last = m.index + m[0].length;
  }
  return out + md.utils.escapeHtml(token.content.slice(last));
};

// Built once, not per render: this runs for every message on every update.
//
// `ADD_ATTR: ['style']` is the deliberate part. Mermaid lays a diagram out with inline
// `style` attributes — an edge's `stroke-dasharray`, the arrow marker's transform — and
// DOMPurify strips them by default, so without this the diagram renders as unconnected
// boxes. It is also the attribute DOMPurify is most careful about, so it is written
// here on purpose rather than inherited by accident.
//
// `foreignObject` + `HTML_INTEGRATION_POINTS` is what makes a diagram's LABELS appear.
// With `htmlLabels: true` mermaid puts each node's text inside a `foreignObject` in the
// XHTML namespace, and DOMPurify drops both by default: the SVG kept its boxes, edges
// and markers and showed nothing written in them. It has to be BOTH settings — the
// element alone comes back empty, because the integration point is what tells DOMPurify
// to keep parsing the XHTML inside it.
//
// This is the one place the sanitizer is widened, so it was measured against a matrix
// of attack vectors rather than assumed: script elements, `img onerror`, `iframe
// src=javascript:`, `svg onload`, `a href=javascript:`, `form action`, `meta refresh`,
// `object data`, and two `mglyph`/`annotation-xml` mXSS breakouts all come back inert
// (0 of 12 leak a live handler or a surviving script element). The residual is a
// `style` attribute whose value reads `url(javascript:...)`, which is inert in every
// browser this page supports — `javascript:` in a CSS url() was an IE-only behaviour —
// and which `ALLOW_DATA_ATTR`/`ADD_ATTR: ['style']` admits for the diagram's geometry
// in the first place.
//
// `SAFE_FOR_XML` is left ON (the default). Turning it off would let the placeholder
// attributes carry their payloads directly, and it was measured: it also lets
// `<noscript><p title="</noscript><img src=x onerror=alert(1)>">` through with its
// handler intact. The payloads are text and the attributes are restored afterwards for
// exactly this reason.
//
// The widening is for the RENDERERS' output only (a KaTeX formula, a mermaid SVG), which
// is what `sanitize` is for. The message itself — HTML the model wrote — goes through
// `sanitizeMessage`, which takes `style` away (a `position:fixed` block can lay a fake
// approval dialog over the real one) and every form control with it: nothing in an
// answer should look like, or be, something to click and type into.
const FORM_TAGS = [
  "form",
  "button",
  "textarea",
  "select",
  "option",
  "optgroup",
  "input",
];

const PURIFY_CONFIG: PurifyConfig = {
  USE_PROFILES: { html: true, svg: true, svgFilters: true, mathMl: true },
  ADD_ATTR: ["style"],
  ALLOW_DATA_ATTR: true,
  // The diagram's node labels live in an XHTML island inside the SVG.
  ADD_TAGS: ["foreignObject"],
  HTML_INTEGRATION_POINTS: { foreignobject: true },
  FORBID_TAGS: FORM_TAGS,
};

/** Sanitizes what a renderer produced (math, diagrams), keeping its inline styles. */
export function sanitize(html: string): string {
  return DOMPurify.sanitize(html, PURIFY_CONFIG);
}

// A separate instance, because DOMPurify's hooks belong to an instance and these two must
// not touch the renderers' output.
const messagePurify = DOMPurify(window);

// The one style a message keeps: a table column's alignment, which markdown-it itself
// writes as `style="text-align:..."`.
const ALIGN_ONLY = /^\s*text-align:\s*(left|right|center)\s*(?:;\s*)?$/i;
messagePurify.addHook("uponSanitizeAttribute", (_node, data) => {
  if (data.attrName === "style" && !ALIGN_ONLY.test(data.attrValue)) {
    data.keepAttr = false;
  }
});

// The one input a message keeps: a task list's checkbox, which markdown-it-task-lists
// renders disabled. It is a picture of a tick, not a control.
messagePurify.addHook("uponSanitizeElement", (node, data) => {
  if (data.tagName !== "input") {
    return;
  }
  const el = node as Element;
  if (el.getAttribute("type") === "checkbox" && el.hasAttribute("disabled")) {
    data.allowedTags.input = true;
  } else {
    data.allowedTags.input = false;
  }
});

const MESSAGE_CONFIG: PurifyConfig = {
  USE_PROFILES: { html: true, svg: true, svgFilters: true, mathMl: true },
  ADD_ATTR: ["style"],
  ALLOW_DATA_ATTR: true,
  FORBID_TAGS: FORM_TAGS.filter((tag) => tag !== "input"),
};

/**
 * Sanitizes the HTML of a message: no free-form styles and no form controls. Each code
 * block then gets the interface's own copy button, which only copies the block.
 */
export function sanitizeMessage(html: string): string {
  const frag = messagePurify.sanitize(html, {
    ...MESSAGE_CONFIG,
    RETURN_DOM_FRAGMENT: true,
  });
  for (const pre of Array.from(frag.querySelectorAll("pre.code-block"))) {
    const button = document.createElement("button");
    button.className = "copy-btn";
    button.textContent = t("copy");
    pre.insertBefore(button, pre.firstChild);
  }
  const box = document.createElement("div");
  box.appendChild(frag);
  return box.innerHTML;
}

// --- The deferred half --------------------------------------------------------

/** The source a placeholder was left carrying, as TEXT. */
function sourceOf(el: HTMLElement): string {
  // An element's textContent is never null.
  return ((el.querySelector("code") ?? el).textContent as string).replace(
    /\n$/,
    "",
  );
}

/**
 * Fills in everything the parse left as a placeholder.
 *
 * Every step is guarded by a `data-done` marker rather than a local variable,
 * because a streaming answer re-renders this whole subtree on every chunk: without
 * the marker the same formula would be re-typeset and the same diagram redrawn on
 * every token that arrives.
 *
 * The attributes the UI depends on are (re)assigned here, from the DOM, AFTER the
 * sanitizer has had its pass. Setting a `dataset` entry assigns a value rather than parsing HTML,
 * so this reintroduces nothing — and it is the only way the copy button can see an
 * answer that contains a comment marker.
 */
async function hydrate(root: HTMLElement): Promise<void> {
  const math = Array.from(
    root.querySelectorAll<HTMLElement>(
      ".math-inline:not([data-done]), .math-block:not([data-done])",
    ),
  );
  const code = Array.from(
    root.querySelectorAll<HTMLElement>("pre.code-block:not([data-done])"),
  );
  const diagrams = Array.from(
    root.querySelectorAll<HTMLElement>(".mermaid-block:not([data-done])"),
  );
  // Emoji is the one step that is not a placeholder: it rewrites text that is already
  // rendered, so its marker goes on the ROOT and it runs on every mount that has not had it.
  const wantsEmoji = root.dataset.emoji !== "ok";
  if (math.length + code.length + diagrams.length === 0 && !wantsEmoji) {
    return;
  }

  // Marked BEFORE any await: two effects can overlap while a module loads, and an
  // unmarked element would be picked up by both.
  for (const el of [...math, ...code, ...diagrams]) {
    el.dataset.done = "pending";
  }

  if (code.length > 0) {
    await hydrateCode(code);
  }
  if (math.length > 0) {
    await hydrateMath(math);
  }
  if (diagrams.length > 0) {
    await hydrateDiagrams(diagrams);
  }

  // LAST, and after every await: the emoji pass rewrites text nodes, and doing it before
  // the math and diagram placeholders were replaced would convert inside content that is
  // about to be thrown away (and, for a formula, inside LaTeX source).
  if (wantsEmoji) {
    await hydrateEmoji(root);
  }
}

// hydrateCode restores the copy payload of the code blocks and colours them.
async function hydrateCode(code: HTMLElement[]): Promise<void> {
  // The copy payload is restored FIRST, before the highlighter replaces the code
  // element's content: it has to be the pristine source either way, and reading it
  // after highlighting would depend on what the grammar chose to wrap.
  for (const el of code) {
    const btn = el.querySelector<HTMLElement>(".copy-btn");
    if (btn) {
      btn.dataset.copyText = sourceOf(el);
    }
  }
  try {
    const { highlight } = await import("./highlight");
    for (const el of code) {
      const target = el.querySelector("code");
      if (!target) {
        continue;
      }
      // The highlighter does its OWN escaping, which is what keeps the code shown and
      // the code copied byte-identical: the copy payload is the pristine source
      // restored above, and the highlighted text is escaped once, from that source.
      target.innerHTML = highlight(sourceOf(el), el.dataset.codeLang || "");
      el.dataset.done = "ok";
    }
  } catch (err) {
    // Uncoloured code is still correct code: nothing is shown to the reader.
    for (const el of code) {
      el.dataset.done = "plain";
    }
    console.info("motita: syntax highlighting is unavailable:", err);
  }
}

// hydrateMath typesets the formulas.
async function hydrateMath(math: HTMLElement[]): Promise<void> {
  try {
    const { renderMath } = await import("./math");
    for (const el of math) {
      // Written back so `fail` and any styling can read it; the DOM is the source.
      const tex = (el.dataset.math || el.textContent || "").trim();
      el.dataset.math = tex;
      el.innerHTML = sanitize(renderMath(tex, el.dataset.display === "1"));
      el.dataset.done = "ok";
    }
  } catch (err) {
    for (const el of math) {
      fail(el, t("the math renderer could not be loaded"), err);
    }
  }
}

// hydrateDiagrams draws the diagrams, each on its own: one that cannot be drawn says so and
// leaves the others alone.
async function hydrateDiagrams(diagrams: HTMLElement[]): Promise<void> {
  try {
    const { renderMermaid } = await import("./mermaid");
    for (const el of diagrams) {
      await drawDiagram(el, renderMermaid);
    }
  } catch (err) {
    for (const el of diagrams) {
      fail(el, t("the diagram renderer could not be loaded"), err);
    }
  }
}

async function drawDiagram(
  el: HTMLElement,
  renderMermaid: (source: string) => Promise<string>,
): Promise<void> {
  const source = sourceOf(el);
  el.dataset.mermaid = source;
  try {
    el.innerHTML = sanitize(await renderMermaid(source));
    el.dataset.done = "ok";
  } catch (err) {
    fail(el, t("this diagram could not be drawn"), err);
  }
}

// hydrateEmoji draws the emoji of the message as images.
async function hydrateEmoji(root: HTMLElement): Promise<void> {
  root.dataset.emoji = "ok";
  try {
    const { renderEmoji } = await import("./emoji");
    root.dataset.emojiCount = String(renderEmoji(root));
  } catch (err) {
    // An emoji that stays a character is exactly as readable as before this existed.
    root.dataset.emoji = "plain";
    console.info("motita: the emoji images are unavailable:", err);
  }
}

/**
 * Shows what could not be rendered as its source plus the reason.
 *
 * An empty box would be worse than showing nothing: the reader would not know
 * whether the answer was wrong or the page was.
 */
function fail(el: HTMLElement, what: string, err: unknown): void {
  const source = el.dataset.math || el.dataset.mermaid || "";
  el.dataset.done = "failed";
  el.classList.add("render-error");
  el.innerHTML =
    "<p>" +
    escapeAttr(what) +
    ".</p><pre>" +
    escapeAttr(source) +
    '</pre><p class="render-error-reason">' +
    escapeAttr(errorMessage(err)) +
    "</p>";
}

/**
 * The full HTML for one message, sanitized.
 *
 * Returned as ONE string, which is what makes an answer cheap to re-render while it
 * streams: there is no virtual DOM diff of every token. The placeholders it contains
 * are filled in by `hydrate`, from the mounted element. The message's own copy payload
 * is set by the component below, after this string is sanitized.
 */
export function renderMessage(source: string): string {
  // The copy button is the interface's own, built here around the sanitized message: the
  // message itself may not contain a button.
  return (
    '<div class="markdown-body">' +
    '<button class="copy-msg-btn">' +
    md.utils.escapeHtml(t("copy")) +
    "</button>" +
    sanitizeMessage(md.render(source)) +
    "</div>"
  );
}

/**
 * releaseMessage puts a built message back to its placeholders: the parsed, sanitized `html`
 * it was mounted with. Only a message that built something heavy (a formula, a diagram, coloured
 * code) is released; the rest have nothing worth dropping, and are left alone. Returns whether
 * the message was released.
 */
export function releaseMessage(
  el: HTMLElement,
  html: string,
  content: string,
): boolean {
  if (!el.querySelector("[data-done]")) {
    return false;
  }
  el.innerHTML = html;
  delete el.dataset.emoji;
  const body = el.querySelector<HTMLElement>(".markdown-body");
  if (body) {
    body.dataset.raw = content;
  }
  return true;
}

export function Markdown({ content }: { content: string }) {
  const ref = useRef<HTMLDivElement>(null);
  // Parsed and sanitized once per content, not once per render. The conversation re-renders on
  // every streamed event, and every message used to be re-parsed by markdown-it and re-sanitized
  // by DOMPurify each time — a long transcript turned each token into a full re-parse of the whole
  // conversation, and the strings and fragments it left behind are what the collector had to chase.
  const html = useMemo(() => renderMessage(content), [content]);

  // A streamed answer is revealed letter by letter as it grows (streamReveal.ts). Layout
  // effects, so the new letters are hidden in the same frame the new HTML is mounted.
  const reveal = useRef<StreamReveal | null>(null);
  useLayoutEffect(() => {
    const r = streamReveal(ref.current!, content);
    reveal.current = r;
    return () => r.dispose();
    // Created once, for the element as mounted; later content goes through `update`.
  }, []);
  const mountedContent = useRef(content);
  useLayoutEffect(() => {
    if (mountedContent.current !== content) {
      mountedContent.current = content;
      reveal.current?.update(content);
    }
  }, [content]);

  useEffect(() => {
    // Effects run after the mount, so the element is there.
    const el = ref.current as HTMLDivElement;
    // The copy button's payload, restored after the sanitizer has run: an answer
    // containing an HTML comment marker or an arrow would otherwise have had this
    // attribute dropped, and the button would copy an empty string.
    const body = el.querySelector<HTMLElement>(".markdown-body");
    if (body) {
      body.dataset.raw = content;
    }

    // The expensive half — formulas, diagrams, code colours, emoji — is DEFERRED until this
    // message is about to be read. Everything stays in the DOM either way: the message is
    // parsed and mounted immediately, so the reader can select the whole conversation as one
    // document. Only the building is postponed, which is what keeps a conversation of a
    // hundred turns from rendering a hundred diagrams nobody has scrolled to.
    //
    // Each of those steps changes the height of the message, and nothing in the component
    // state changes while it happens, so a scroll that reacts to state alone stops wherever it
    // was and the page ends up short of the bottom. Announcing the end of the work is what lets
    // the container follow it — fired on the element so the listener stays scoped to this
    // message's subtree, and bubbling so it reaches the conversation container.
    return watchForHydration(
      el,
      () => {
        const announce = () =>
          el.dispatchEvent(
            new CustomEvent("motita:hydrated", { bubbles: true }),
          );
        return hydrate(el).then(announce, announce);
      },
      () => releaseMessage(el, html, content),
    );
  }, [content]);

  return <div ref={ref} dangerouslySetInnerHTML={{ __html: html }} />;
}
