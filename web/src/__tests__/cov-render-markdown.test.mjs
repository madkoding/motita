// The Markdown an answer is rendered with, and its deferred half: code colours (highlight.ts),
// formulas (math.ts), diagrams (mermaid.ts, with mermaid itself replaced by a fake that records
// what it was asked) and emoji (emoji.ts). Runs the real modules in jsdom.
// Run with: node --test web/src/__tests__/cov-render-markdown.test.mjs
import test from "node:test";
import assert from "node:assert/strict";
import { setupDom, load, flush, text } from "./cov-render-helpers.mjs";

// The fake mermaid: it records the configuration and every render, draws a small SVG (with a
// script in it, which the sanitizer must take out) and refuses a source that says "bad".
const FAKE_MERMAID = `
const calls = (globalThis.__mermaid = { init: [], render: [] });
export default {
  initialize(config) { calls.init.push(config); },
  async render(id, source) {
    calls.render.push([id, source]);
    if (source.includes("bad")) throw new Error("Parse error on line 2");
    return { svg: '<svg id="' + id + '" style="max-width: 100px"><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">label</div></foreignObject><script>alert(1)</script></svg>' };
  },
};
`;

const dom = setupDom({ overrides: { mermaid: FAKE_MERMAID } });

// A manual IntersectionObserver: the test says when a message comes near the viewport and when
// it goes far away. hydration.ts creates two (near, 600px margin; far, 4000px).
const observers = [];
globalThis.IntersectionObserver = class {
  constructor(cb, opts) {
    this.cb = cb;
    this.opts = opts;
    this.els = new Set();
    observers.push(this);
  }
  observe(el) {
    this.els.add(el);
  }
  unobserve(el) {
    this.els.delete(el);
  }
  disconnect() {}
};
const near = () => observers.find((o) => o.opts.rootMargin === "600px 0px");
const far = () => observers.find((o) => o.opts.rootMargin === "4000px 0px");
const report = (obs, el, isIntersecting) =>
  obs.cb([{ target: el, isIntersecting }]);

const md = await load("Markdown.tsx");
const { highlight } = await load("highlight.ts");
const { renderMath } = await load("math.ts");
const { renderMermaid } = await load("mermaid.ts");
const { renderEmoji } = await load("emoji.ts");
const { CODEPOINTS } = await load("emoji-codes.ts");
const { cancelQuiet } = await load("hydration.ts");
const hljs = (await import("highlight.js/lib/core")).default;
const { h, render } = await import("preact");
const { act } = await import("preact/test-utils");

// body parses a rendered message into a detached element to look at.
function body(html) {
  const d = document.createElement("div");
  d.innerHTML = html;
  return d;
}

// mount draws a Markdown component, brings it into view and waits for its deferred half.
async function mount(content) {
  const root = document.createElement("div");
  document.body.appendChild(root);
  const hydrated = [];
  root.addEventListener("motita:hydrated", () => hydrated.push(1));
  await act(() => render(h(md.Markdown, { content }), root));
  const el = root.firstElementChild;
  report(near(), el, true);
  await flush(10);
  return {
    root,
    el,
    hydrated,
    async update(next) {
      await act(() => render(h(md.Markdown, { content: next }), root));
      report(near(), root.firstElementChild, true);
      await flush(10);
    },
    async done() {
      await act(() => render(null, root));
      root.remove();
      cancelQuiet();
    },
  };
}

// ── the parse ─────────────────────────────────────────────────────────────────

test("a message has the interface's copy button and CommonMark's own rules", () => {
  const d = body(
    md.renderMessage(
      "# Title\n\nline one  \nline two\n\n\\*not italic\\* <https://x.test>",
    ),
  );
  const wrap = d.querySelector(".markdown-body");
  assert.equal(wrap.firstElementChild.className, "copy-msg-btn");
  assert.equal(wrap.firstElementChild.textContent, "copy");
  assert.equal(d.querySelector("h1").textContent, "Title");
  assert.ok(d.querySelector("p br"), "two trailing spaces are a hard break");
  assert.match(d.querySelector("p:last-child").textContent, /^\*not italic\*/);
  assert.equal(d.querySelector("a").getAttribute("href"), "https://x.test");
});

test("$$ blocks become display formulas, on one line or across several", () => {
  const one = body(md.renderMessage("$$ x^2 $$"));
  assert.equal(one.querySelector(".math-block").textContent, "x^2");
  assert.equal(one.querySelector(".math-block").dataset.display, "1");

  const multi = body(md.renderMessage("$$\na + b\n= c\n$$\n\nafter"));
  assert.equal(multi.querySelector(".math-block").textContent, "a + b\n= c");
  assert.equal(multi.querySelector("p").textContent, "after");

  const opened = body(md.renderMessage("$$ a\nb $$"));
  assert.equal(opened.querySelector(".math-block").textContent, "a\nb");

  // A paragraph is ended by a formula block that starts on the next line.
  const ends = body(md.renderMessage("some text\n$$ y $$"));
  assert.equal(ends.querySelector("p").textContent, "some text");
  assert.equal(ends.querySelector(".math-block").textContent, "y");
});

test("an unclosed $$ stays as the reader wrote it", () => {
  const d = body(md.renderMessage("$$ open\nand never closed"));
  assert.equal(d.querySelector(".math-block"), null);
  assert.match(d.querySelector("p").textContent, /^\$\$ open/);
});

test("inline $...$ is a formula; money and stray dollars stay text", () => {
  const d = body(
    md.renderMessage("Area $a<b$ costs $ 5, $$b$$ too, plain end"),
  );
  const spans = d.querySelectorAll(".math-inline");
  assert.equal(spans.length, 1);
  assert.equal(spans[0].textContent, "a<b");
  assert.match(
    text(d.querySelector("p")),
    /costs \$ 5, \$\$b\$\$ too, plain end$/,
  );
  assert.equal(
    text(body(md.renderMessage("no dollars")).querySelector("p")),
    "no dollars",
  );
});

test("prices are not formulas; one-character and spaced-inside formulas still are", () => {
  // Each line is [markdown, the formulas it holds].
  const cases = [
    ["It costs $5 and $10 now", []],
    ["between $5 and$10", []],
    ["from $5 to $20, or $x$ each", ["x"]],
    ["$x$", ["x"]],
    ["let $a + b$ be, and $5$ too", ["a + b", "5"]],
    ["a $ x$ and $y $ stay", []],
  ];
  for (const [src, formulas] of cases) {
    const d = body(md.renderMessage(src));
    const got = [...d.querySelectorAll(".math-inline")].map(
      (s) => s.textContent,
    );
    assert.deepEqual(got, formulas, src);
    if (formulas.length === 0) {
      assert.equal(text(d.querySelector("p")), src);
    }
  }
});

test("fences become diagram, formula and code placeholders carrying their source as text", () => {
  const src = [
    "```mermaid",
    "graph LR; A --> B",
    "```",
    "```math",
    "\\frac{1}{2}",
    "```",
    "```JS extra",
    'const a = "<b>";',
    "```",
    "```",
    "plain",
    "```",
  ].join("\n");
  const d = body(md.renderMessage(src));
  assert.equal(
    d.querySelector(".mermaid-block").textContent,
    "graph LR; A --> B",
  );
  assert.equal(d.querySelector("div.math-block").textContent, "\\frac{1}{2}");
  const [js, plain] = d.querySelectorAll("pre.code-block");
  assert.equal(js.dataset.codeLang, "js");
  assert.equal(js.querySelector("code").className, "lang-js hljs");
  assert.equal(js.querySelector("code").textContent, 'const a = "<b>";');
  assert.equal(
    js.firstElementChild.className,
    "copy-btn",
    "the interface adds the copy button",
  );
  assert.equal(plain.dataset.codeLang, "");
  assert.equal(plain.querySelector("code").className, "hljs");
});

test("a message keeps table alignment, task ticks and harmless HTML, but no styles or controls", () => {
  const src = [
    "| a | b |",
    "|:--|--:|",
    "| 1 | 2 |",
    "",
    "- [x] done",
    "- [ ] todo",
    "",
    '<div style="position:fixed;top:0">fake dialog</div>',
    "",
    '<p style="text-align: center;">centred</p>',
    "",
    '<kbd>Ctrl</kbd> <button>click</button> <input type="text" value="x"> <input type="checkbox">',
    "",
    '<form action="/x"><textarea>t</textarea><select><option>o</option></select></form>',
    "",
    "<img src=x onerror=alert(1)>",
  ].join("\n");
  const d = body(md.renderMessage(src));
  const ths = d.querySelectorAll("th");
  assert.equal(ths[0].getAttribute("style"), "text-align:left");
  assert.equal(ths[1].getAttribute("style"), "text-align:right");
  const ticks = d.querySelectorAll("input");
  assert.equal(
    ticks.length,
    2,
    "only the two disabled task checkboxes survive",
  );
  for (const tick of ticks) {
    assert.equal(tick.type, "checkbox");
    assert.ok(tick.disabled);
  }
  assert.ok(ticks[0].checked);
  const fake = [...d.querySelectorAll("div")].find(
    (x) => x.textContent === "fake dialog",
  );
  assert.equal(fake.hasAttribute("style"), false);
  const centred = [...d.querySelectorAll("p")].find(
    (x) => x.textContent === "centred",
  );
  assert.equal(centred.getAttribute("style"), "text-align: center;");
  assert.equal(d.querySelector("kbd").textContent, "Ctrl");
  assert.equal(d.querySelectorAll("button:not(.copy-msg-btn)").length, 0);
  assert.equal(d.querySelector("form, textarea, select, option"), null);
  assert.equal(d.querySelector("img").hasAttribute("onerror"), false);
});

test("sanitize keeps a renderer's styles and diagram labels, and nothing executable", () => {
  const out = body(
    md.sanitize(
      '<svg style="stroke-dasharray: 3"><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">A</div></foreignObject>' +
        '<script>alert(1)</script></svg><button>b</button><img src=x onerror="alert(1)">',
    ),
  );
  assert.equal(
    out.querySelector("svg").getAttribute("style"),
    "stroke-dasharray: 3",
  );
  assert.equal(
    out.querySelector("foreignObject, foreignobject").textContent,
    "A",
  );
  assert.equal(out.querySelector("script"), null);
  assert.equal(out.querySelector("button"), null);
  assert.equal(out.querySelector("img").hasAttribute("onerror"), false);
});

// ── the deferred modules on their own ─────────────────────────────────────────

test("highlight escapes once, colours a known language and falls back for the rest", () => {
  assert.equal(highlight("a --> <b>", ""), "a --&gt; &lt;b&gt;");
  assert.equal(highlight('x = "&"', "cobol"), "x = &quot;&amp;&quot;");
  const js = highlight("const x = 1; // -->", "js");
  assert.match(js, /<span class="hljs-keyword">const<\/span>/);
  assert.match(js, /--&gt;/);
  assert.doesNotMatch(js, /&amp;gt;/, "never escaped twice");
  for (const lang of [
    "go",
    "python",
    "ts",
    "sh",
    "yaml",
    "json",
    "sql",
    "css",
    "html",
    "xml",
    "yml",
    "shell",
    "typescript",
    "javascript",
    "bash",
  ]) {
    assert.ok(hljs.getLanguage(lang), lang);
  }
});

test("a grammar that throws still shows the code, escaped", () => {
  const saved = hljs.highlight;
  hljs.highlight = () => {
    throw new Error("grammar blew up");
  };
  try {
    assert.equal(highlight("if (a < b) {}", "go"), "if (a &lt; b) {}");
  } finally {
    hljs.highlight = saved;
  }
});

test("renderMath typesets MathML without the TeX fallback text, and survives a bad formula", () => {
  const inline = renderMath("x^2", false);
  assert.match(inline, /<math/);
  assert.doesNotMatch(inline, /<annotation/);
  assert.doesNotMatch(inline, /display="block"/);
  assert.match(renderMath("\\sum_i i", true), /display="block"/);
  const bad = renderMath("\\frac{", false);
  assert.match(
    bad,
    /katex-error|\\frac/,
    "the source is shown with the reason, not thrown",
  );
});

test("renderMermaid initialises mermaid once, strictly, and gives each diagram its own id", async () => {
  const a = await renderMermaid("graph LR; A --> B");
  const b = await renderMermaid("graph TD; C");
  const calls = globalThis.__mermaid;
  assert.equal(calls.init.length, 1);
  const cfg = calls.init[0];
  assert.equal(cfg.securityLevel, "strict");
  assert.equal(cfg.startOnLoad, false);
  assert.equal(cfg.suppressErrorRendering, true);
  assert.equal(cfg.htmlLabels, true);
  const [id1, id2] = calls.render.slice(-2).map((r) => r[0]);
  assert.match(id1, /^motita-diagram-\d+$/);
  assert.notEqual(id1, id2);
  assert.match(a, new RegExp(`id="${id1}"`));
  assert.match(b, new RegExp(`id="${id2}"`));
  await assert.rejects(renderMermaid("bad diagram"), /Parse error/);
});

// ── emoji ─────────────────────────────────────────────────────────────────────

function emojiIn(html) {
  const d = document.createElement("div");
  d.innerHTML = html;
  const n = renderEmoji(d);
  return { n, d, imgs: [...d.querySelectorAll("img.emoji")] };
}

test("the asset list is the generated codepoint set", () => {
  const set = CODEPOINTS.split(",");
  assert.ok(set.length > 1000);
  assert.ok(set.includes("1f600"));
  assert.ok(set.includes("31-20e3"));
});

test("emoji with an asset become images; the text around them stays", () => {
  const { n, d, imgs } = emojiIn("<p>hi 😀 and ❤️ end</p>");
  assert.equal(n, 2);
  assert.deepEqual(
    imgs.map((i) => i.getAttribute("src")),
    ["/emoji/1f600.webp", "/emoji/2764.webp"],
  );
  assert.equal(imgs[0].alt, "😀");
  assert.equal(imgs[0].getAttribute("loading"), "lazy");
  assert.equal(imgs[0].getAttribute("draggable"), "false");
  assert.equal(d.querySelector("p").textContent, "hi  and  end");
});

test("whole sequences are matched as one picture, keycaps included", () => {
  const { imgs } = emojiIn("<p>🏳️‍🌈🐈‍⬛1️⃣</p>");
  assert.deepEqual(
    imgs.map((i) => i.getAttribute("src")),
    [
      "/emoji/1f3f3-200d-1f308.webp",
      "/emoji/1f408-200d-2b1b.webp",
      "/emoji/31-20e3.webp",
    ],
  );
});

test("emoji without an asset, and lone selectors, stay characters", () => {
  const { n, d } = emojiIn("<p>flag 🇨🇱 sel ️ joiner ‍</p>");
  assert.equal(n, 0);
  assert.equal(d.querySelector("p").textContent, "flag 🇨🇱 sel ️ joiner ‍");
  const mixed = emojiIn("<p>🇨🇱😀</p>");
  assert.equal(mixed.n, 1);
  assert.equal(
    mixed.d.querySelector("p").textContent,
    "🇨🇱",
    "the flag is left whole",
  );
});

test("code, diagrams, formulas and SVG keep their characters", () => {
  const { n } = emojiIn(
    '<pre>😀</pre><code>😀</code><kbd>😀</kbd><div class="mermaid-block"><span>😀</span></div>' +
      '<span class="math-inline">😀</span><svg><text>😀</text></svg><p>plain words</p>',
  );
  assert.equal(n, 0);
});

test("text that is not inside an element is left alone", () => {
  const frag = document.createDocumentFragment();
  frag.appendChild(document.createTextNode("😀 loose"));
  assert.equal(renderEmoji(frag), 0);
  assert.equal(frag.textContent, "😀 loose");
});

// ── the component ─────────────────────────────────────────────────────────────

test("a mounted message builds its code, formulas, diagrams and emoji once it comes into view", async () => {
  const content = [
    "Intro 😀 with $x^2$ inline.",
    "",
    "```js",
    "const a = 1; // -->",
    "```",
    "",
    "$$ \\sqrt{2} $$",
    "",
    "```mermaid",
    "graph LR; A --> B",
    "```",
    "",
    "```mermaid",
    "bad diagram",
    "```",
  ].join("\n");
  const m = await mount(content);
  const { el, hydrated } = m;
  assert.equal(
    el.querySelector(".markdown-body").dataset.raw,
    content,
    "the copy payload survives -->",
  );

  const pre = el.querySelector("pre.code-block");
  assert.equal(pre.dataset.done, "ok");
  assert.equal(
    pre.querySelector(".copy-btn").dataset.copyText,
    "const a = 1; // -->",
  );
  assert.match(pre.querySelector("code").innerHTML, /hljs-keyword/);

  const inline = el.querySelector(".math-inline");
  assert.equal(inline.dataset.done, "ok");
  assert.equal(inline.dataset.math, "x^2");
  assert.ok(inline.querySelector("math"));
  assert.equal(
    el
      .querySelector(".math-block")
      .querySelector("math")
      .getAttribute("display"),
    "block",
  );

  const [good, bad] = el.querySelectorAll(".mermaid-block");
  assert.equal(good.dataset.done, "ok");
  assert.equal(good.dataset.mermaid, "graph LR; A --> B");
  assert.ok(good.querySelector("svg"));
  assert.equal(good.querySelector("script"), null);
  assert.equal(bad.dataset.done, "failed");
  assert.ok(bad.classList.contains("render-error"));
  assert.equal(
    bad.querySelector("p").textContent,
    "this diagram could not be drawn.",
  );
  assert.equal(bad.querySelector("pre").textContent, "bad diagram");
  assert.equal(
    bad.querySelector(".render-error-reason").textContent,
    "Parse error on line 2",
  );

  assert.equal(el.dataset.emoji, "ok");
  assert.equal(el.dataset.emojiCount, "1");
  assert.equal(el.querySelector("img.emoji").alt, "😀");
  assert.equal(hydrated.length, 1, "the end of the work is announced");
  assert.equal(el.dataset.hydrating, undefined);
  await m.done();
});

test("a message far out of view goes back to its placeholders, and builds again on return", async () => {
  const m = await mount("```js\nlet x\n```");
  const { el } = m;
  assert.equal(el.querySelector("pre").dataset.done, "ok");
  el.getBoundingClientRect = () => ({ height: 120 });
  report(near(), el, false);
  report(far(), el, false);
  assert.equal(
    el.querySelector("pre").dataset.done,
    undefined,
    "the colours are dropped",
  );
  assert.equal(el.dataset.emoji, undefined);
  assert.equal(
    el.querySelector(".markdown-body").dataset.raw,
    "```js\nlet x\n```",
  );
  assert.equal(el.style.minHeight, "120px");
  report(near(), el, true);
  await flush(10);
  assert.equal(el.querySelector("pre").dataset.done, "ok");
  // A message with nothing heavy built is not released.
  report(near(), el, false);
  el.querySelector("pre").removeAttribute("data-done");
  report(far(), el, false);
  assert.equal(el.dataset.released, undefined);
  await m.done();
});

test("new content re-renders the message; with nothing left to build it is announced at once", async () => {
  const m = await mount("first 😀");
  assert.equal(m.el.dataset.emoji, "ok");
  await m.update("second, plain");
  const el = m.root.firstElementChild;
  assert.equal(text(el.querySelector(".markdown-body")), "copysecond, plain");
  assert.equal(el.querySelector(".markdown-body").dataset.raw, "second, plain");
  assert.equal(m.hydrated.length, 2);
  await m.done();
});

test("a model-written placeholder is filled from what it carries", async () => {
  const m = await mount(
    '<span class="math-inline" data-math="y^3">ignored</span> <span class="math-inline"></span>\n\n' +
      '<pre class="code-block">no code element</pre>\n\n<pre class="code-block"><code>raw()</code></pre>',
  );
  const [given, empty] = m.el.querySelectorAll(".math-inline");
  assert.equal(given.dataset.math, "y^3");
  assert.equal(given.dataset.done, "ok");
  assert.equal(empty.dataset.math, "");
  const [raw, coded] = m.el.querySelectorAll("pre.code-block");
  assert.equal(
    raw.dataset.done,
    "pending",
    "nothing to colour in a block with no code element",
  );
  // With no code element the block's whole text is the payload, the button's label included.
  assert.equal(
    raw.querySelector(".copy-btn").dataset.copyText,
    "copyno code element",
  );
  // One without a language is shown escaped and uncoloured.
  assert.equal(coded.dataset.done, "ok");
  assert.equal(coded.querySelector("code").innerHTML, "raw()");
  assert.equal(coded.querySelector(".copy-btn").dataset.copyText, "raw()");
  await m.done();
});

test("releaseMessage leaves an unbuilt message alone and restores a built one", () => {
  const el = document.createElement("div");
  el.innerHTML = "<p>plain</p>";
  assert.equal(md.releaseMessage(el, "<p>x</p>", "x"), false);
  assert.equal(el.innerHTML, "<p>plain</p>");

  el.innerHTML = '<div data-done="ok">built</div>';
  el.dataset.emoji = "ok";
  assert.equal(md.releaseMessage(el, "<p>bare</p>", "bare"), true);
  assert.equal(el.innerHTML, "<p>bare</p>");
  assert.equal(el.dataset.emoji, undefined);

  el.innerHTML = '<div data-done="ok">built</div>';
  const html = md.renderMessage("**b**");
  assert.equal(md.releaseMessage(el, html, "**b**"), true);
  assert.equal(el.querySelector(".markdown-body").dataset.raw, "**b**");
  assert.equal(el.querySelector("strong").textContent, "b");
});
