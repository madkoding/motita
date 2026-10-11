"""Measure the Markdown rendering of an answer, in a real browser, against a gateway
this check owns.

The defects this exists to catch are all invisible in the markup and in the source:

  * an attribute carrying a diagram's source, silently emptied by the sanitizer because
    the source contained `-->` — the diagram box drew and said nothing;
  * a formula's raw LaTeX printed under the typeset formula, because the sanitizer
    dropped the annotation element but kept its text;
  * a diagram's labels missing entirely while its boxes, edges and arrow markers all
    rendered, because of an upstream setting in the diagram library.

Each of those reads as "it works" from the DOM's shapes alone. So every assertion here
is against a RENDERED value — a bounding box, a computed colour, a text node — and never
against a class name.

Usage:
    GATEWAY_URL=http://127.0.0.1:PORT GATEWAY_STATE=<token file> CDP_PORT=... \
        ./cdp_markdown.py
"""

import asyncio
import base64
import json
import os
import pathlib
import sys
import tempfile

# The fixture is the document this renderer is judged on, and it is deliberately the
# hard one: every CommonMark corner case plus the extensions an answer actually uses.
#
# Two things in it are load-bearing rather than decorative:
#
#   * every diagram arrow is `-->`, and so is the code sample. That marker is what
#     `SAFE_FOR_XML` reacts to, and it silently emptied the attributes that carried a
#     diagram's source and the copy button's payload. Without a `-->` in the fixture the
#     gate would pass over a page whose diagram says nothing and whose copy button
#     copies nothing.
#   * the code sample is short on purpose: the copy payload is asserted against the
#     LENGTH OF THE CODE ITSELF, so a fixed threshold would break the day the sample is
#     edited, and both numbers come from the rendered page.
#
# spanish-fixture: the whole point of this document is that it is Spanish prose.
EXAMPLE = """## Saltos y escapes

Línea uno  
Línea dos (con dos espacios al final).

\\esto no es cursiva\\

Aquí una nota.[^1]

[^1]: Texto de la nota al pie.

<kbd>Ctrl</kbd> + <kbd>C</kbd>, <mark>resaltado</mark>, <sub>sub</sub>, <sup>sup</sup>.

Término
: Definición del término.

> [!NOTE]
> Nota informativa.

> [!WARNING]
> Aviso importante.

En línea: $E = mc^2$

En bloque:

$$
\\int_0^\\infty e^{-x^2}\\,dx = \\frac{\\sqrt{\\pi}}{2}
$$

Una fórmula que exige mucho más del motor: una matriz, una suma con límites, un
límite, una raíz, un sumatorio doble y letras griegas.

$$
\\begin{pmatrix} a & b \\\\ c & d \\end{pmatrix}
\\qquad
\\sum_{n=1}^{\\infty} \\frac{1}{n^2} = \\frac{\\pi^2}{6}
\\qquad
\\lim_{x \\to 0} \\frac{\\sin x}{x} = 1
\\qquad
\\sqrt[3]{\\frac{\\alpha \\cdot \\beta}{\\gamma}}
$$

```mermaid
graph LR
  A[Inicio] --> B[Fin]
```

```mermaid
sequenceDiagram
  participant U as Usuario
  participant S as Servidor
  U->>S: petición
  S-->>U: respuesta
```

```mermaid
gantt
  title Plan
  dateFormat YYYY-MM-DD
  section Fase
  trabajo :a1, 2026-01-01, 30d
```

```mermaid
pie title Reparto
  "uno" : 60
  "dos" : 40
```

```js
const arrow = a --> b
```

Emoji: caras 🙂 🥳 🚀 ❤️ 🔥, bandera arcoiris 🏳️‍🌈, gato negro 🐈‍⬛, teclas 1️⃣ 2️⃣, una sin asset 🇨🇱 y otra sin asset 🏴󠁧󠁢󠁳󠁣󠁴󠁿.

Dentro de código no debe convertirse: `const x = "🔥"` ni en un bloque:

```
echo "🚀 despegar"
```

Y en cursiva *🦀* junto a texto normal.

Y un diagrama que NO compila, para probar qué se muestra cuando el diagrama está mal:

```mermaid
graph LR
  A[Inicio --> B[Fin]
```
"""


class CDP:
    """A CDP client with exactly ONE coroutine reading the socket.

    Two readers raise `cannot call recv while another coroutine is already running
    recv`, and a collector task alongside the request loop is the usual way to write
    that bug.
    """

    def __init__(self, ws):
        self.ws = ws
        self.i = 0

    async def call(self, method, **params):
        self.i += 1
        mid = self.i
        await self.ws.send(json.dumps({"id": mid, "method": method, "params": params}))
        while True:
            msg = json.loads(await self.ws.recv())
            if msg.get("id") == mid:
                if "error" in msg:
                    raise RuntimeError(f"{method}: {msg['error']}")
                return msg.get("result", {})

    async def js(self, expr):
        r = await self.call(
            "Runtime.evaluate", expression=expr, returnByValue=True, awaitPromise=True
        )
        if r.get("exceptionDetails"):
            raise RuntimeError(str(r["exceptionDetails"])[:900])
        return r.get("result", {}).get("value")

    async def shot(self, path):
        r = await self.call("Page.captureScreenshot", format="png")
        await asyncio.to_thread(pathlib.Path(path).write_bytes, base64.b64decode(r["data"]))
        return path


# What the page ended up holding, read from the DOM and from computed styles. Kept as
# one expression so the page is interrogated once, in a consistent state.
MEASURE = r"""
(() => {
  const box = [...document.querySelectorAll('.markdown-body')]
    .find(b => b.textContent.includes('Salto'));
  if (!box) return {error: 'the answer is not rendered at all'};
  const q = s => box.querySelectorAll(s).length;
  const text = box.textContent;

  // 1. two trailing spaces are a HARD LINE BREAK: a real <br>, and the paragraph is
  //    taller than one line.
  const p1 = [...box.querySelectorAll('p')].find(p => p.textContent.includes('Línea uno'));
  const brCount = p1 ? p1.querySelectorAll('br').length : 0;
  const p1Height = p1 ? Math.round(p1.getBoundingClientRect().height) : 0;

  // 2. the escaped asterisks stay literal. Asserted by TEXT, not by counting <em>:
  //    the answer legitimately has 14 other emphasis runs.
  const escapePara = [...box.querySelectorAll('p')]
    .find(p => p.textContent.includes('esto no es cursiva'));
  const escapeText = escapePara ? escapePara.textContent.trim() : null;
  const escapeHasEm = escapePara ? escapePara.querySelectorAll('em').length : -1;

  // 3. inline HTML reaches the DOM, and <mark> is actually painted.
  const markBg = (() => {
    const el = box.querySelector('mark');
    return el ? getComputedStyle(el).backgroundColor : null;
  })();

  // 4. footnotes: the reference links to a real target that exists.
  const fnRef = box.querySelector('a[href^="#fn"]');
  const fnTarget = fnRef ? box.querySelector(fnRef.getAttribute('href')) : null;
  const fnListed = q('section.footnotes li') > 0;

  // 5. definition list.
  const dl = box.querySelector('dl');
  const dt = dl ? (dl.querySelector('dt') || {}).textContent : null;
  const dd = dl ? (dl.querySelector('dd') || {}).textContent : null;

  // 6. MATH: real <math> elements, sized, and NO raw TeX left showing. This is the
  //    assertion that catches the orphaned annotation text: the element count alone
  //    would pass on a page that also printed the LaTeX underneath.
  const maths = [...box.querySelectorAll('math')];
  const mathGeom = maths.map(m => {
    const r = m.getBoundingClientRect();
    return {w: Math.round(r.width), h: Math.round(r.height),
            display: m.getAttribute('display') || 'inline'};
  });
  const blockMath = box.querySelector('math[display="block"]');
  const rawTex = ['\\int_0', '\\frac', '\\sqrt', '\\infty'].filter(t => text.includes(t));
  const annotationLeak = maths.some(m => /\\[a-zA-Z]+/.test(m.textContent));

  // 7. DIAGRAMS: every diagram on the page, drawn, with visible LABELS. The label
  //    text is the whole point: the SVG renders empty boxes without it, which every
  //    shape-based check passes. All four are checked, because the reason mermaid is
  //    the full build is that a reader can put ANY diagram type in an answer.
  const blocks = [...box.querySelectorAll('.mermaid-block')];
  const diagrams = blocks.map(b => {
    const s = b.querySelector('svg');
    const r = s ? s.getBoundingClientRect() : null;
    const lab = [...b.querySelectorAll('.nodeLabel')]
      .map(n => n.textContent.trim()).filter(Boolean);
    // A label can also be a plain SVG <text> (sequence, gantt, pie all draw theirs
    // that way), so the visible-ink test covers both rather than trusting one class.
    const texts = [...b.querySelectorAll('text')]
      .map(t => t.textContent.trim()).filter(Boolean);
    return {
      done: b.getAttribute('data-done'),
      svg: !!s, w: r ? Math.round(r.width) : 0, h: r ? Math.round(r.height) : 0,
      // The reason lives in .render-error-reason (see fail() in Markdown.tsx).
      error: b.classList.contains('render-error')
        ? ((b.querySelector('.render-error-reason') || {}).textContent || '').slice(0, 300)
          + ' || source: ' + ((b.querySelector('pre') || {}).textContent || '').slice(0, 80)
        : null,
      labels: lab, texts,
      edges: b.querySelectorAll('.edgePaths path, path.flowchart-link').length,
      arrowMarker: [...b.querySelectorAll('path')].some(p => p.getAttribute('marker-end')),
      actors: [...b.querySelectorAll('.actor')].map(a => a.textContent.trim()).filter(Boolean),
      tasks: [...b.querySelectorAll('.taskText, .sectionTitle')]
        .map(t => t.textContent.trim()).filter(Boolean),
      slices: [...b.querySelectorAll('.pieCircle, .slice')].length,
    };
  });
  const mm = blocks[0] || null;

  // 8. alerts render as callouts, not as blockquotes with the word NOTE inside.
  const alerts = [...box.querySelectorAll('.markdown-alert')].map(e => e.className);

  // 9. code: highlighted, and the copy payload is non-empty EVEN THOUGH the snippet
  //    contains `-->` — the case the sanitizer emptied.
  const codeBtn = box.querySelector('pre.code-block .copy-btn');
  const copyText = codeBtn ? codeBtn.getAttribute('data-copy-text') || '' : null;
  const token = box.querySelector('pre.code-block code span[class*=hljs]');
  const tokenColor = token ? getComputedStyle(token).color : null;
  const codeText = box.querySelector('pre.code-block code');

  // 10. the message's own copy payload, which contains `-->` too.
  const msgRaw = box.getAttribute('data-raw') || '';

  // 11. EMOJI: converted to images in the text, and NOT inside code. The second half is
  //    the assertion that matters: a conversion that runs over a code block would corrupt
  //    the snippet the reader copies, which is a defect worse than not having the feature.
  const emojiImgs = [...box.querySelectorAll('img.emoji')];
  const emojiInText = emojiImgs.filter(i => !i.closest('pre, code')).length;
  const emojiInCode = emojiImgs.filter(i => i.closest('pre, code')).length;
  const codeEmojiLeft = [...box.querySelectorAll('pre code')]
    .some(c => /[\u{1F300}-\u{1FAFF}]/u.test(c.textContent));
  const emojiGeom = emojiImgs.slice(0, 4).map(i => {
    const r = i.getBoundingClientRect();
    const cs = getComputedStyle(i);
    return {w: Math.round(r.width), h: Math.round(r.height), src: i.getAttribute('src'),
            alt: i.alt, loaded: i.complete && i.naturalWidth > 0,
            // A character, not a framed picture: the generic `.markdown-body img` rule styles
            // a screenshot, and any of its box properties reaching an emoji shows up as a
            // stray border with padding. The font size is carried alongside so the size can
            // be judged against the text rather than against a number that means nothing here.
            border: cs.borderTopWidth, radius: cs.borderTopLeftRadius, margin: cs.marginLeft,
            pad: cs.paddingTop, display: cs.display, va: cs.verticalAlign,
            font: cs.fontSize};
  });
  // hydrate() runs on the element the component rendered, which is the PARENT of
  // `.markdown-body`, so the marker is looked up from the box outwards rather than read off
  // the box: reading it off the box directly is how this reported "the pass did not run"
  // while fifteen emoji were already rendered as images.
  const emojiHost = box.closest('[data-emoji]') || box;
  const emojiState = emojiHost.getAttribute('data-emoji');
  const emojiCount = emojiHost.getAttribute('data-emoji-count') || box.getAttribute('data-emoji-count');
  // A ZWJ sequence (the rainbow flag, the black cat) is ONE picture, and a country flag has
  // no FluentUI asset at all: both must survive as their original characters rather than
  // becoming a series of images that shows half a sequence or a broken image.
  const emojiAlt = emojiImgs.map(i => i.alt);
  const textLeft = box.textContent || '';
  const flagLeft = textLeft.includes('🇨🇱');
  const zwjWhole = emojiAlt.includes('🏳️‍🌈') && emojiAlt.includes('🐈‍⬛')
  const zwjSplit = emojiAlt.some(a => a === '🏳' || a === '🌈' || a === '🐈' || a === '⬛')
  // A keycap (`1️⃣`) and a tag-sequence flag (Scotland) are both multi-codepoint too, and
  // both are cases where a lookup that misses looks identical to a character with no asset.
  const keycapOk = emojiAlt.includes('1️⃣') && emojiAlt.includes('2️⃣')
  const tagFlagLeft = textLeft.includes('\u{1F3F4}\u{E0067}\u{E0062}\u{E0073}\u{E0063}\u{E0074}\u{E007F}')
  const blackFlagShown = emojiAlt.includes('🏴')

  return {
    lineBreak: {br: brCount, paragraphHeight: p1Height},
    escape: {text: escapeText, emInside: escapeHasEm},
    inlineHtml: {kbd: q('kbd'), mark: q('mark'), sub: q('sub'), sup: q('sup'), markBg},
    footnotes: {refs: q('a[href^="#fn"]'), targetExists: !!fnTarget, listed: fnListed},
    deflist: {dl: !!dl, dt, dd},
    math: {count: maths.length, geom: mathGeom,
           blockVisible: blockMath ? blockMath.getBoundingClientRect().height > 0 : false,
           rawTexShowing: rawTex, annotationLeak},
    mermaid: {present: !!mm, svg: !!mm && !!mm.querySelector('svg'),
              w: mm && mm.querySelector('svg')
                 ? Math.round(mm.querySelector('svg').getBoundingClientRect().width) : 0,
              h: mm && mm.querySelector('svg')
                 ? Math.round(mm.querySelector('svg').getBoundingClientRect().height) : 0,
              done: mm ? mm.getAttribute('data-done') : null},
    diagrams,
    alerts,
    code: {copyTextLen: copyText === null ? null : copyText.length,
           copyText, tokenColor,
           codeLen: codeText ? codeText.textContent.length : 0},
    messageCopyLen: msgRaw.length,
    emoji: {state: emojiState, count: emojiCount, total: emojiImgs.length,
            inText: emojiInText, inCode: emojiInCode, codeEmojiLeft,
            alt: emojiAlt, flagLeft, zwjWhole, zwjSplit,
            keycapOk, tagFlagLeft, blackFlagShown,
            geom: emojiGeom},
    renderErrors: q('.render-error'),
  };
})()
"""


def main():
    import websockets

    base = os.environ["GATEWAY_URL"]
    token = open(os.environ["GATEWAY_STATE"]).read().strip()
    cdp_port = os.environ.get("CDP_PORT", "9355")
    shots = os.environ.get("SHOTS_DIR") or tempfile.mkdtemp(prefix="motita-markdown-")
    os.makedirs(shots, exist_ok=True)

    # The gateway URL is resolved to a live WebSocket, so this probe never starts a
    # browser of its own: the caller owns that, the same way verify-spinner.sh does.
    from cdp_http import fetch_json

    targets = fetch_json(f"http://127.0.0.1:{cdp_port}/json/list", timeout=5)
    page = next((t for t in targets if t.get("type") == "page"), None)
    if not page:
        print("no page target in the browser")
        return 2

    return asyncio.run(run(page["webSocketDebuggerUrl"], base, token, shots))


async def run(ws_url, base, token, shots):
    import websockets

    failures = []
    notes = []

    def bad(msg):
        failures.append(msg)
        print(f"  FAIL  {msg}")

    def ok(msg):
        print(f"  ok    {msg}")

    def note(msg):
        # A fact worth printing that is not a verdict: the probe must never FAIL on data
        # it was given (a shortened quiet window means a state was not sampled, not a defect).
        notes.append(msg)
        print(f"  note  {msg}")

    async with websockets.connect(ws_url, max_size=80 * 1024 * 1024) as ws:
        c = CDP(ws)
        await c.call("Page.enable")
        await c.call("Runtime.enable")
        await c.call("Network.enable")
        # The service worker serves the OLD shell. Without this, every measurement
        # describes the previous build and reads as "my change did nothing".
        await c.call("Network.setCacheDisabled", cacheDisabled=True)

        # about:blank FIRST: a navigation that differs only in the FRAGMENT does not
        # reload, so going straight to the token URL twice leaves the first page in place.
        await c.call("Page.navigate", url="about:blank")
        await c.call("Page.navigate", url=f"{base}/#t={token}")
        await asyncio.sleep(2.5)
        await c.js("""(async () => {
            for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
            for (const k of await caches.keys()) await caches.delete(k);
        })()""")
        await c.call("Page.navigate", url="about:blank")
        # A recorder that outlives the navigation it observes: it watches the conversation container
        # from the first frame, so "was it hidden WHILE loading?" is answered from the page's own
        # history instead of being inferred from a sample taken later.
        await c.call(
            "Page.addScriptToEvaluateOnNewDocument",
            source="""
            (() => {
              const log = [];
              window.__visLog = log;
              const frames = [];
              window.__frameLog = frames;
              let lastKey = null;
              const tick = () => {
                const el = document.querySelector('main[role="log"]');
                const scrim = document.querySelector('.chat-modal-scrim');
                const loading = el ? el.getAttribute('data-chat-loading') : null;
                // Per-FRAME, and every frame: a transition is many intermediate values, a jump is
                // one. Thinned by the key so the array stays small, but the key includes the
                // opacity so every intermediate step is kept.
                const key = (el ? getComputedStyle(el).opacity : '-') + '|' + loading + '|' +
                            (scrim ? getComputedStyle(scrim).opacity : '-');
                if (key !== lastKey) {
                  lastKey = key;
                  frames.push({
                    at: Math.round(performance.now()),
                    chat: el ? getComputedStyle(el).opacity : null,
                    loading: loading,
                    modal: scrim ? getComputedStyle(scrim).opacity : null,
                    scrollTop: el ? Math.round(el.scrollTop) : null,
                    scrollBehavior: el ? getComputedStyle(el).scrollBehavior : null,
                    msg: (() => {
                      const c = el && el.firstElementChild;
                      return c ? getComputedStyle(c).opacity : null;
                    })(),
                  });
                }
                if (el) {
                  const cs = getComputedStyle(el);
                  const stable = cs.opacity + '|' + loading + '|' + el.scrollHeight + '|' +
                                 el.scrollTop;
                  if (stable !== lastKey) {
                    // Everything that could move the scroll when the conversation changes size.
                    // Attributed per element so a resize is NAMED instead of guessed at.
                    const box = (n) => {
                      if (!n) return null;
                      const r = n.getBoundingClientRect();
                      return { h: Math.round(r.height), top: Math.round(r.top) };
                    };
                    const parent = el.parentElement;
                    log.push({
                      at: Math.round(performance.now()), opacity: cs.opacity,
                      loading: loading, modal: !!scrim,
                      contentHeight: el.scrollHeight,
                      boxHeight: Math.round(el.getBoundingClientRect().height),
                      scrollTop: Math.round(el.scrollTop),
                      clientHeight: el.clientHeight,
                      parent: box(parent),
                      parentClient: parent ? parent.clientHeight : null,
                      header: box(document.querySelector('header')),
                      aside: box(document.querySelector('aside')),
                      docHeight: document.documentElement.scrollHeight,
                      innerHeight: window.innerHeight,
                    });
                  }
                }
              };
              setInterval(tick, 16);
            })();
            """,
        )
        await c.call("Page.navigate", url=f"{base}/#t={token}")
        await asyncio.sleep(3)

        # The seeded conversation, opened by clicking its row the way a reader would.
        #
        # The observation is installed BEFORE the click, and that is the only place it can be:
        # the spinner exists exactly while the conversation is being put on screen, so a check
        # that looks for it afterwards is looking for something that has deliberately gone.
        #
        # It watches the MUTATION RECORDS rather than re-querying the document, so a node that
        # is added and removed between two callbacks is still seen. The description is taken
        # from the live element whenever one is present, because `getComputedStyle` on a
        # detached node reports nothing — which is what the colour check would read.
        await c.js(r"""(() => {
          const log = window.__loadLog = { spinner: null, seen: 0, clearedAt: null,
                                            heights: [], samples: 0 };
          const describe = (wrap) => {
            const svg = wrap.querySelector('svg');
            const path = svg && svg.querySelector('path');
            const anim = path && path.querySelector('animateTransform');
            const r = svg ? svg.getBoundingClientRect() : null;
            return {
              label: (wrap.querySelector('.chat-modal-label') || {}).textContent || null,
              d: path ? path.getAttribute('d') : null,
              fill: path ? getComputedStyle(path).fill : null,
              w: r ? Math.round(r.width) : 0,
              h: r ? Math.round(r.height) : 0,
              rotate: anim ? anim.getAttribute('values') : null,
              dur: anim ? anim.getAttribute('dur') : null,
              repeat: anim ? anim.getAttribute('repeatCount') : null,
              role: (wrap.querySelector('.chat-modal') || wrap).getAttribute('role'),
              live: !!wrap.isConnected,
              // The card, and the area it sits in: a small modal is a much smaller box inside
              // the scrim, which is also how the two designs are told apart in a measurement.
              modal: (() => {
                const card = wrap.querySelector('.chat-modal');
                const cr = card ? card.getBoundingClientRect() : null;
                const sr = wrap.getBoundingClientRect();
                return {
                  w: cr ? Math.round(cr.width) : 0,
                  h: cr ? Math.round(cr.height) : 0,
                  scrimW: Math.round(sr.width),
                  scrimH: Math.round(sr.height),
                };
              })(),
            };
          };
          const note = (wrap) => {
            log.seen++;
            if (wrap.isConnected) log.spinner = describe(wrap);
          };
          const sample = () => {
            const m = document.querySelector('main[role="log"]');
            log.samples++;
            // Only once the conversation has content: the height before that says nothing
            // about whether the deferred work resized the page.
            if (m && document.querySelector('.markdown-body')) {
              log.heights.push(m.scrollHeight);
              // Height AND position: a gap that survives a scroll the reader is following is
              // either a scroll that did not land or a page that grew again afterwards, and the
              // two need opposite fixes. Recording both is what tells them apart.
              log.geom = log.geom || [];
              if (log.geom.length < 60) log.geom.push({
                t: Math.round(performance.now()),
                h: m.scrollHeight, top: Math.round(m.scrollTop), c: m.clientHeight,
                gap: m.scrollHeight - Math.round(m.scrollTop) - m.clientHeight,
              });
            }
          };
          // Sampled on a timer as well as on mutations: the growth that was stranding the reader
          // raised no event at all, so a mutation-driven sampler cannot see it.
          log.timer = setInterval(sample, 120);
          const io = window.__ioLog = { callbacks: 0, decisions: [] };
          // The real observer is wrapped so its callbacks are recorded: a `data-inview` of 1 is
          // otherwise indistinguishable from "the observer never reported and the default stuck".
          const RealIO = window.IntersectionObserver;
          window.IntersectionObserver = class extends RealIO {
            constructor(cb, opts) {
              super((records, ob) => {
                io.callbacks++;
                if (io.opts === undefined) io.opts = opts && {rootMargin: opts.rootMargin, threshold: opts.threshold};
                for (const r of records) {
                  const b = r.boundingClientRect, rb = r.rootBounds;
                  io.decisions.push({
                    hit: r.isIntersecting,
                    top: Math.round(b.top), h: Math.round(b.height),
                    rootTop: rb ? Math.round(rb.top) : null,
                    rootBottom: rb ? Math.round(rb.bottom) : null,
                  });
                }
                if (io.decisions.length > 40) io.decisions = io.decisions.slice(-40);
                cb(records, ob);
              }, opts);
            }
          };

          const obs = new MutationObserver((records) => {
            for (const rec of records) {
              for (const n of rec.addedNodes) {
                if (n.nodeType !== 1) continue;
                if (n.matches('.chat-modal-scrim')) { note(n); continue; }
                const inner = n.querySelector && n.querySelector('.chat-modal-scrim');
                if (inner) note(inner);
              }
              if (log.clearedAt === null) {
                for (const n of rec.removedNodes) {
                  if (n.nodeType !== 1) continue;
                  const gone = n.matches('.chat-modal-scrim') ||
                               (n.querySelector && n.querySelector('.chat-modal-scrim'));
                  if (gone && log.seen) {
                    log.clearedAt = performance.now();
                    const hosts = [...document.querySelectorAll('[data-hydrating]')];
                    const vis = hosts.filter(h => h.getAttribute('data-inview') !== '0');
                    log.atClear = {
                      hostsPending: hosts.length,
                      visiblePending: vis.length,
                      hydratedSoFar: (window.__idleLog || {}).hydratedCount || 0,
                    };
                  }
                }
              }
            }
            // A spinner still on screen is described again from the live node: the first
            // sighting may arrive in a batch where nothing has been laid out yet.
            const now = document.querySelector('.chat-modal-scrim');
            if (now) note(now);
            sample();
          });
          obs.observe(document.body, {
            childList: true, subtree: true, attributes: true,
            attributeFilter: ['data-hydrating'],
          });
          sample();
          // A second, tiny recorder for the timing question: did the modal come down only
          // AFTER the last message finished? `__loadLog` already times the clear; this counts
          // the completions and stamps the last one, plus when the page called itself idle.
          const idle = window.__idleLog = { hydratedCount: 0, lastHydratedAt: null, idleAt: null };
          document.addEventListener('motita:hydrated', () => {
            idle.hydratedCount++;
            idle.lastHydratedAt = performance.now();
          }, true);
          document.addEventListener('motita:chat-idle', () => {
            if (idle.idleAt === null) idle.idleAt = performance.now();
          });
          window.__loadLogStop = () => obs.disconnect();
          return true;
        })()""")

        opened = False
        for _ in range(8):
            rows = await c.js("""(() => [...document.querySelectorAll('.session-row')].map((r, i) => ({
                i, title: (r.querySelector(':scope > div.flex-1 > div') || {}).textContent?.trim() || ''
            })))()""")
            target = next((r["i"] for r in rows if "Markdown" in r["title"]), None)
            if target is None:
                break
            await c.js(f"document.querySelectorAll('.session-row')[{target}].click()")
            for _ in range(24):
                await asyncio.sleep(0.5)
                if await c.js("""(() => [...document.querySelectorAll('.markdown-body')]
                      .some(b => b.textContent.includes('Salto')))()"""):
                    opened = True
                    break
            if opened:
                break
        if not opened:
            bad("the seeded conversation could not be opened in the interface")
            await c.shot(f"{shots}/not-opened.png")
            print("\nVERDICT: FAILED (the page was never reached)")
            return 1
        ok("the seeded answer is rendered in the interface")

        # The example is the LAST turn, so it is off the bottom of a long conversation where it
        # would stay UNBUILT (that is the point of the observer). Scroll to the end the way a
        # reader would — this is also what starts its hydration, since entering the viewport is
        # what triggers it. Without this the whole measurement below would run against a message
        # the reader has not reached yet.
        await c.js("""(() => {
            const el = document.querySelector('main[role="log"]');
            if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'auto' });
            return true;
        })()""")
        await asyncio.sleep(0.5)

        # On-demand modules: wait for the diagram to finish rather than guessing.
        for _ in range(40):
            if await c.js("document.querySelectorAll('.mermaid-block svg').length"):
                break
            await asyncio.sleep(0.5)
        await asyncio.sleep(1.5)

        m = await c.js(MEASURE)
        await asyncio.to_thread(pathlib.Path(f"{shots}/measured.json").write_text, json.dumps(m, indent=2, ensure_ascii=False))
        await c.shot(f"{shots}/rendered.png")

        if "error" in m:
            bad(m["error"])
            print("\nVERDICT: FAILED (nothing measured)")
            return 1

        # --- line breaks ------------------------------------------------------
        if m["lineBreak"]["br"] >= 1 and m["lineBreak"]["paragraphHeight"] > 20:
            ok(f"two trailing spaces make a hard break "
               f"({m['lineBreak']['br']} <br>, paragraph {m['lineBreak']['paragraphHeight']}px tall)")
        else:
            bad(f"two trailing spaces did not break the line: "
                f"{m['lineBreak']['br']} <br>, paragraph {m['lineBreak']['paragraphHeight']}px")

        # --- escapes ----------------------------------------------------------
        esc = m["escape"]
        if esc["text"] == "\\esto no es cursiva\\" and esc["emInside"] == 0:
            ok("an escaped pair of asterisks stays literal, with no emphasis")
        else:
            bad(f"the escaped asterisks were not kept literal: {esc['text']!r} "
                f"with {esc['emInside']} <em> inside")

        # --- inline HTML ------------------------------------------------------
        html = m["inlineHtml"]
        if html["kbd"] == 2 and html["mark"] == 1 and html["sub"] == 1 and html["sup"] == 2:
            ok(f"inline HTML renders ({html['kbd']} kbd, {html['mark']} mark, "
               f"{html['sub']} sub, {html['sup']} sup)")
        else:
            bad(f"inline HTML did not render as elements: {html}")
        if html["markBg"] and html["markBg"] not in ("rgba(0, 0, 0, 0)", "transparent"):
            ok(f"<mark> is actually painted ({html['markBg']})")
        else:
            bad(f"<mark> has no background colour ({html['markBg']}): the tag is there "
                f"but nothing shows")

        # --- footnotes --------------------------------------------------------
        fn = m["footnotes"]
        if fn["refs"] >= 1 and fn["targetExists"] and fn["listed"]:
            ok(f"a footnote is a working reference ({fn['refs']} ref, target resolves, listed)")
        else:
            bad(f"the footnote did not become a working reference: {fn}")

        # --- definition list --------------------------------------------------
        dl = m["deflist"]
        if dl["dl"] and dl["dt"] and dl["dd"]:
            ok(f"the definition list renders (dt={dl['dt']!r}, dd={dl['dd']!r})")
        else:
            bad(f"the definition list did not render as a <dl>: {dl}")

        # --- math -------------------------------------------------------------
        math = m["math"]
        if math["count"] >= 3 and math["blockVisible"]:
            ok(f"the formulas are typeset as MathML ({math['count']} <math>, "
               f"block height > 0): {math['geom']}")
        else:
            bad(f"the formulas were not typeset: {math}")
        if math["rawTexShowing"] or math["annotationLeak"]:
            bad(f"the RAW LaTeX is still visible ({math['rawTexShowing']} / "
                f"annotation text inside <math>: {math['annotationLeak']}) — the "
                f"accessible annotation was dropped but its text was left behind")
        else:
            ok("no raw LaTeX is printed beside the typeset formula")

        # --- diagrams: every type, drawn AND labelled -------------------------
        # The point of the full mermaid build is that ANY diagram type works, so one
        # type rendering is not evidence. Each entry names the ink that proves its own
        # type drew: a flowchart has node labels and an edge, a sequence has actors, a
        # gantt has task/section text, a pie has slices.
        diagrams = m["diagrams"]
        expected = [
            ("flowchart", lambda d: d["labels"] == ["Inicio", "Fin"] and d["edges"] >= 1,
             "node labels Inicio/Fin and an edge"),
            ("sequenceDiagram", lambda d: "Usuario" in d["actors"] and "Servidor" in d["actors"],
             "both actors"),
            ("gantt", lambda d: any("trabajo" in t for t in d["tasks"]),
             "the task text"),
            ("pie", lambda d: d["slices"] >= 2, "its slices"),
        ]
        # FOUR diagram types plus the one malformed diagram at the end of the fixture.
        if len(diagrams) != len(expected) + 1:
            bad(f"expected {len(expected)} diagrams plus the malformed one on the page, "
                f"found {len(diagrams)}")
        # The malformed diagram is the one that proves the failure path: it must be reported
        # by THIS renderer (a .render-error box with the reason and the offending source), not
        # by mermaid drawing its own "Syntax error in text" icon. Suppressing that icon is why
        # it is `suppressErrorRendering: true` in mermaid.ts.
        valid, broken = diagrams[:len(expected)], diagrams[len(expected):]
        if len(broken) != 1:
            bad(f"expected 1 deliberately-broken diagram, found {len(broken)}: the failure "
                f"path is not being exercised, so this check would pass on a page that "
                f"shows mermaid's own error icon")
        else:
            b = broken[0]
            if b["error"]:
                ok(f"a malformed diagram is reported by the renderer, not by mermaid "
                   f"(reason: {(b['error'] or '').split(' || ')[0][:70]!r})")
            else:
                bad(f"the malformed diagram did not produce this renderer's error box "
                    f"(done={b['done']!r}): mermaid's own error icon is what a reader would "
                    f"see instead of the reason and the source")
        mermaidOwn = await c.js("""(() => ({
          inBody: [...document.body.querySelectorAll('svg')]
            .filter(s => /Syntax error in text/.test(s.textContent || '')).length,
          version: document.body.textContent.includes('mermaid version'),
          inBlock: [...document.querySelectorAll('.mermaid-block svg')]
            .filter(s => /Syntax error in text/.test(s.textContent || '')).length,
        }))()""")
        if mermaidOwn["inBody"] or mermaidOwn["version"]:
            bad(f"mermaid's own error icon is on the page ({mermaidOwn}): it draws a red X "
                f"with 'Syntax error in text' and its version, and that path does not clean "
                f"up its temporary element — which is how it ends up outside the block")
        else:
            ok("no 'Syntax error in text' icon anywhere on the page, inside or outside a block")
        for (name, check, what), d in zip(expected, valid):
            if d["error"]:
                bad(f"the {name} diagram failed to render: {d['error']}")
            elif not d["svg"] or d["w"] == 0 or d["h"] == 0:
                bad(f"the {name} diagram has no drawn size ({d['w']}x{d['h']})")
            elif not check(d):
                bad(f"the {name} diagram drew no {what}: labels={d['labels']} "
                    f"actors={d['actors']} tasks={d['tasks']} slices={d['slices']} "
                    f"texts={d['texts'][:6]}")
            else:
                ok(f"the {name} diagram draws its own content ({what}), "
                   f"{d['w']}x{d['h']}, done={d['done']}")

        # The exact regression this file exists for: empty boxes with a live arrow.
        mm = m["mermaid"]
        if not (mm["present"] and mm["svg"]):
            bad("the first diagram was not drawn at all")
        elif not diagrams or diagrams[0]["labels"] != ["Inicio", "Fin"]:
            bad(f"the diagram's node labels are missing (boxes, edges and markers can "
                f"all render with no text in them): {diagrams[0]['labels'] if diagrams else None}")

        # --- emoji: images in the text, characters in the code ------------------
        # The first half proves the feature ran; the second is the one that would catch a
        # conversion walking into a code block and corrupting what the reader copies.
        em = m["emoji"]
        if em["state"] != "ok":
            bad(f"the emoji pass did not run (data-emoji={em['state']!r})")
        elif em["inText"] < 6:
            bad(f"only {em['inText']} emoji became images (expected at least 6: the "
                f"characters, the ZWJ sequences, the one in emphasis): {em['geom']}")
        else:
            ok(f"emoji in the text render as FluentUI images ({em['inText']} of "
               f"{em['total']}, data-emoji-count={em['count']}): {em['geom']}")
        # A ZWJ sequence is ONE emoji and has ONE asset: split into its parts it would show
        # a picture of something the answer did not say.
        if em["zwjSplit"]:
            bad(f"a ZWJ sequence was split into separate images: {em['alt']}")
        elif em["zwjWhole"]:
            ok("a ZWJ sequence stays one image (rainbow flag, black cat)")
        else:
            bad(f"the ZWJ sequences did not render as one image each: {em['alt']}")
        # A character with no FluentUI asset (a country flag) must stay a character: it is
        # the font's glyph or nothing, and an <img> pointing at a file that does not exist
        # would be a broken picture.
        if em["flagLeft"]:
            ok("an emoji with no asset (a country flag) stays a character, not a broken image")
        else:
            bad("the country flag was converted or dropped: an emoji with no asset must fall "
                "back to the font's own glyph")
        # A tag-sequence flag shares its base codepoint with the plain black flag, so a
        # sequence that is not consumed whole degrades to the WRONG picture and no lookup
        # miss can be seen. Both halves are asserted.
        if em["blackFlagShown"]:
            bad("the England flag was drawn as the plain black flag: its tag characters were "
                "not part of the match, so the lookup named the base codepoint")
        elif em["tagFlagLeft"]:
            ok("a tag-sequence flag stays its own characters instead of becoming a wrong image")
        else:
            bad("the tag-sequence flag vanished: it was neither converted nor left as characters")
        if em["keycapOk"]:
            ok("multi-codepoint keycaps convert (1️⃣ 2️⃣)")
        else:
            bad(f"the keycaps did not convert — a key that misses looks identical to a "
                f"character with no asset: {em['alt']}")
        if em["inCode"] > 0:
            bad(f"{em['inCode']} emoji were converted INSIDE code: the snippet the reader "
                f"copies is now an <img> instead of a character")
        elif em["codeEmojiLeft"]:
            ok("emoji inside code stay characters, as they must")
        else:
            bad("no emoji was left in the code to prove the skip works: the fixture no "
                "longer exercises it, so this check would pass on a conversion that "
                "walked into code blocks")
        imgs = [g for g in em["geom"] if g["src"]]
        if len(imgs) >= 3 and all(g["loaded"] and g["w"] > 0 and g["h"] > 0 for g in imgs):
            ok(f"the emoji images load and are sized from the font ({imgs[0]['w']}x"
               f"{imgs[0]['h']} for {imgs[0]['src']})")
        else:
            bad(f"the emoji images did not load or have no size: {em['geom']}")
        # An emoji is a CHARACTER, not a framed picture. The generic `.markdown-body img` rule
        # styles a pasted screenshot with a border, a radius and an outer margin, and all three
        # reach an emoji through inheritance of the plain selector — which is a stray box drawn
        # around every emoji in the text.
        framed = [g for g in imgs
                  if g["border"] not in ("0px", "") or g["radius"] not in ("0px", "")
                  or g["pad"] not in ("0px", "")]
        if not imgs:
            bad("no emoji images to check for a stray frame")
        elif not framed:
            ok(f"an emoji is drawn as a bare character: no border ({imgs[0]['border']}), "
               f"no radius ({imgs[0]['radius']}), no padding ({imgs[0]['pad']})")
        else:
            bad(f"{len(framed)} emoji(s) carry the screenshot rule's box — border "
                f"{framed[0]['border']}, radius {framed[0]['radius']}, padding "
                f"{framed[0]['pad']}: at least one emoji is inside a box")
        # Larger than the text it sits in: an emoji glyph is drawn well above the cap height of
        # the surrounding letters, and an image sized to the font exactly reads as small next
        # to one. Judged against the computed font size rather than a pixel count, so it holds
        # at any font size the reader picks.
        sized = [g for g in imgs if g["h"] >= 1.15 * float(g["font"].replace("px", ""))]
        if not imgs:
            pass
        elif sized:
            ok(f"emoji are drawn larger than the text they sit in ({imgs[0]['h']}px tall "
               f"against a {imgs[0]['font']} font)")
        else:
            bad(f"emoji are not larger than the text: {imgs[0]['h']}px against a "
                f"{imgs[0]['font']} font (want at least 1.15em)")

        # --- alerts -----------------------------------------------------------
        kinds = sorted("note" if "note" in a else "warning" for a in m["alerts"] if "markdown-alert" in a)
        if kinds == ["note", "warning"]:
            ok("both callouts render as styled alerts (note + warning)")
        else:
            bad(f"the callouts did not render as alerts: {m['alerts']}")

        # --- code and the copy contract ---------------------------------------
        code = m["code"]
        if code["tokenColor"]:
            ok(f"code is highlighted (a token is painted {code['tokenColor']})")
        else:
            bad("no highlighted token: the code block is plain text")
        if (code["copyTextLen"] is not None and code["codeLen"] > 0
                and code["copyTextLen"] == code["codeLen"]):
            ok(f"the code block's copy button holds its source exactly "
               f"({code['copyTextLen']} chars) even though the snippet contains -->")
        elif code["copyTextLen"]:
            bad(f"the code block's copy payload does not match the code it shows: "
                f"{code['copyTextLen']} chars of payload for {code['codeLen']} chars of code")
        else:
            bad(f"the code block's copy payload is empty ({code['copyTextLen']}) — the "
                f"sanitizer emptied it")
        if m["messageCopyLen"] > 100:
            ok(f"the message's copy payload survives ({m['messageCopyLen']} chars) even "
               f"though it contains -->")
        else:
            bad(f"the message's own copy payload is missing "
                f"({m['messageCopyLen']} chars): copying an answer would paste nothing")

        # --- nothing failed to render, EXCEPT the one that is meant to ---------
        # The fixture ends with a malformed diagram on purpose, so exactly one error box is
        # the expected count. Asserting 0 here would be asserting the failure path is never
        # exercised, which is the state that let mermaid's own error icon reach the page.
        if m["renderErrors"] == 1:
            ok("exactly one element is an error box: the deliberately-broken diagram, and "
               "nothing else")
        elif m["renderErrors"] == 0:
            bad("no element reported a render failure, but the fixture contains a malformed "
                "diagram: it failed silently, which is worse than showing the reason")
        else:
            bad(f"{m['renderErrors']} elements rendered as error boxes; only the one "
                f"deliberately-broken diagram should be")

        # --- the loading spinner over the conversation -------------------------
        ll = await c.js("window.__loadLog")
        await c.js("window.__loadLogStop && window.__loadLogStop()")
        await asyncio.to_thread(pathlib.Path(f"{shots}/loadlog.json").write_text, json.dumps(ll, indent=2, ensure_ascii=False))
        sp = (ll or {}).get("spinner") or {}
        if not ll or ll.get("seen", 0) < 1:
            bad("the chat spinner never appeared while the conversation was loading")
        elif not sp.get("live"):
            bad("the chat spinner appeared but was detached before it could be measured")
        else:
            ok(f"the chat spinner covers the conversation while it loads ({ll['seen']} sightings, "
               f"{sp['w']}x{sp['h']})")
            if sp.get("role") == "status":
                ok("the spinner announces itself to assistive tech (role=status)")
            else:
                bad(f"the spinner carries no role: {sp.get('role')!r}")
            if sp.get("label"):
                ok(f"the spinner says what it is doing: {sp['label']!r}")
            else:
                bad("the spinner has no label")
            # The mark asked for: this exact path, painted in hsl(228, 97%, 42%).
            # That converts to rgb(3, 45, 211) — measured, not assumed: a first version of this
            # check hardcoded the conversion wrong (rgb(3, 14, 231), which is closer to hue 232)
            # and failed a spinner that was painted correctly.
            if (sp.get("d") or "").startswith("M12,23a9.63"):
                ok("the spinner draws the arc that was asked for")
            else:
                bad(f"the spinner's path is not the one asked for: {(sp.get('d') or '')[:40]!r}")
            if sp.get("fill") == "rgb(3, 45, 211)":
                ok(f"the spinner is painted the requested colour ({sp['fill']})")
            else:
                bad(f"the spinner's colour is {sp.get('fill')!r}, not the requested "
                    f"hsl(228, 97%, 42%) = rgb(3, 45, 211)")
            if sp.get("rotate") == "0 12 12;360 12 12" and sp.get("dur") == "0.75s" \
                    and sp.get("repeat") == "indefinite":
                ok(f"the spinner turns ({sp['rotate']} in {sp['dur']}, {sp['repeat']})")
            else:
                bad(f"the spinner does not turn as asked: values={sp.get('rotate')!r} "
                    f"dur={sp.get('dur')!r} repeat={sp.get('repeat')!r}")
            # A small modal, not a full-bleed overlay: the card has to be much smaller than the
            # area it sits in, which is also what tells the two designs apart in a measurement.
            modal = sp.get("modal") or {}
            if modal.get("w") and modal.get("h") and \
                    modal["w"] < 420 and modal["h"] < 140 and modal.get("scrimW", 0) > modal["w"]:
                ok(f"the spinner is a small modal card ({modal['w']}x{modal['h']} inside a "
                   f"{modal['scrimW']}x{modal['scrimH']} area), not a full-bleed overlay")
            else:
                bad(f"the spinner is not presented as a small modal: {modal}")

        # --- the deferred work only happens for what is about to be READ ----------
        # The page must not build every diagram of every turn on mount. The conversation seeded
        # here is deliberately long for this: the filler turns sit far above the fold and must
        # still be UNBUILT when the transcript is up, while the message the reader is on is built.
        #
        # Judged in the DOM (`data-inview`, `data-hydrating`) rather than by counting SVGs: the
        # number of nodes is what is being kept down, so counting them is the measurement, but
        # WHICH ones is a property of the observer. Both are reported.
        obs = await c.js("""(() => {
            // The host element that carries the hydration markers is the one hydrate() received.
            const hosts = [...document.querySelectorAll('[data-hydrating], [data-inview]')];
            const blocks = [...document.querySelectorAll('.mermaid-block')];
            const drawn = blocks.filter(b => b.querySelector('svg'));
            // "The reader sees a diagram" is about what is on SCREEN, so the drawn blocks are
            // counted by whether they are inside the viewport — not merely present in the DOM,
            // which is what the deferral makes meaningful in the first place.
            return {
              hosts: hosts.length,
              // The SAME mark the observer maintains, read as the count on the page: this is
              // what the spinner's visible-pending question is answered from, so it is the
              // attribute that has to be right in both directions.
              inView: hosts.filter(h => h.getAttribute('data-inview') === '1').length,
              outOfView: hosts.filter(h => h.getAttribute('data-inview') === '0').length,
              stillPending: hosts.filter(h => h.hasAttribute('data-hydrating')).length,
              // The message the reader is ON: is anything in it still unbuilt? That is the
              // requirement, and it is not the same question as "is a diagram visible" — the
              // block in front of the reader here is the deliberately-broken one, which must
              // NOT be drawn.
              inViewPending: hosts.filter(h => h.getAttribute('data-inview') === '1'
                                              && h.hasAttribute('data-hydrating')).length,
              mermaidBlocks: blocks.length,
              mermaidDrawn: drawn.length,
              scrollTop: Math.round((document.querySelector('main[role="log"]') || {}).scrollTop || 0),
            };
        })()""")
        await asyncio.to_thread(pathlib.Path(f"{shots}/observer.json").write_text, json.dumps(obs, indent=2, ensure_ascii=False))
        if not obs or obs.get("hosts", 0) < 2:
            bad(f"no messages registered for deferred work: {obs}")
        else:
            ok(f"{obs['hosts']} message(s) registered for deferred hydration "
               f"({obs['inView']} in view, {obs['outOfView']} out of view)")
            # The load-bearing one. Both halves are needed: an eager build makes every message
            # "in view" (outOfView 0), and an observer that never reported leaves every message
            # at whatever it was initialised to — which looks identical from this side.
            both = obs["inView"] > 0 and obs["outOfView"] > 0
            if both:
                ok(f"visibility is a real measurement with both outcomes ({obs['inView']} in "
                   f"view, {obs['outOfView']} out of view) — messages off screen are left UNBUILT, "
                   f"so a long conversation does not render every diagram it holds on mount")
            else:
                bad(f"every one of the {obs['hosts']} message(s) reads as {('in view' if obs['outOfView'] == 0 else 'out of view')}"
                    f" (inView={obs['inView']}, outOfView={obs['outOfView']}): either everything "
                    f"was hydrated regardless of visibility, or the observer never reported")
            # And the flip side: what the reader IS looking at must be built, or the deferral is
            # just a page that never renders.
            if obs["inViewPending"] == 0:
                ok(f"the message in front of the reader is fully built (nothing in view is still "
                   f"pending), with {obs['mermaidDrawn']} of {obs['mermaidBlocks']} diagram(s) "
                   f"drawn on the page")
            else:
                bad(f"{obs['inViewPending']} message(s) in view are still unbuilt — the deferral "
                    f"never fired for what the reader is looking at")
            # And the other half of the same coin: the messages the reader never passed are still
            # unbuilt, which is the POINT. Asserting that every diagram is drawn would be
            # asserting that everything was hydrated, i.e. that the deferral does nothing.
            if obs["mermaidDrawn"] < obs["mermaidBlocks"]:
                ok(f"only the diagrams the reader reached were built ({obs['mermaidDrawn']} of "
                   f"{obs['mermaidBlocks']}): nothing else was paid for")
            else:
                bad(f"every one of the {obs['mermaidBlocks']} diagram(s) was built even though the "
                    f"reader jumped past most of them — the deferral did nothing")

        # --- the modal waits for the page to STOP, and does not flicker --------------
        #
        # The design is a QUIET WINDOW: activity restarts a 2s timer, and the modal only comes down
        # when that timer runs out. So the reader's question ("did it wait for everything?") and the
        # flicker question are answered by the same two measurements:
        #
        #   * was the modal taken down at all, and only once nothing visible was still being built;
        #   * how many times was it PUT UP — one is the design working, one per answer is the
        #     "loading, finishing, loading again" the reader reported.
        cleared_now = None
        for _ in range(40):
            cleared_now = await c.js("document.querySelector('.chat-modal-scrim') === null")
            if cleared_now:
                break
            await asyncio.sleep(0.5)
        vis_log = await c.js("window.__visLog || []")
        frames = await c.js("window.__frameLog || []")
        await asyncio.to_thread(pathlib.Path(f"{shots}/visibility-log.json").write_text, json.dumps(vis_log, indent=2, ensure_ascii=False))
        hidden = [x for x in (vis_log or []) if float(x.get("opacity", 1)) == 0]
        visible = [x for x in (vis_log or []) if float(x.get("opacity", 1)) > 0.9]
        while_loading = [x for x in (vis_log or []) if x.get("loading") == "1"]
        at_full = [x for x in while_loading if float(x.get("opacity", 0)) > 0.9]
        hidden = [x for x in (vis_log or []) if float(x.get("opacity", 1)) == 0]
        revealed = [x for x in (vis_log or []) if float(x.get("opacity", 1)) > 0.9
                    and x.get("loading") == "0"]
        # Two things this must NOT claim, both of which a naive reading gets wrong:
        #
        #   * The page is legitimately at full opacity BEFORE a session starts loading (measured:
        #     opacity 1, loading 0, no modal at 50ms). That is not a "reveal", so ordering the two
        #     states against each other tests nothing.
        #   * The fade is intentional (`transition: opacity 180ms`), so the first frames after
        #     loading begins are still on their way down (measured 1 -> 0.46 -> 0.099 -> 0 over
        #     ~150ms). What the reader must never see is a FULLY-opaque transcript while the parse
        #     is still running, once the fade has had time to finish.
        fade_ms = await c.js("""(() => {
            const el = document.querySelector('main[role="log"]');
            if (!el) return 0;
            const d = getComputedStyle(el).transitionDuration || '0s';
            // '0.42s' or '420ms'; take the first value and normalise to ms.
            const first = d.split(',')[0].trim();
            const n = parseFloat(first) || 0;
            return Math.round(first.endsWith('ms') ? n : n * 1000);
        })()""")
        # The allowance is the fade PLUS a margin for the frame cadence: the assertion is that the
        # reader never sees the transcript at full opacity while it is being built, not that the
        # browser finishes a CSS transition on an exact millisecond.
        FADE_MS = (fade_ms or 0) + 150
        while_loading = [x for x in (vis_log or []) if x.get("loading") == "1"]
        hidden = [x for x in (vis_log or []) if float(x.get("opacity", 1)) == 0]
        settled = (vis_log or [{}])[-1]
        if not vis_log:
            bad("nothing recorded the conversation's visibility while the page loaded")
        elif not while_loading:
            bad(f"the conversation never reported itself as loading: {vis_log}")
        elif not hidden:
            bad(f"the conversation was NEVER held at opacity 0 while it loaded - the reader "
                f"watches it change shape. Transitions: {vis_log}")
        else:
            # Each LOADING WINDOW is judged on its own, because there is more than one in a real
            # session (the transcript being replaced, then the conversation being opened) and a
            # threshold computed from the first window's start flagged every sample of the second
            # one as "still visible". A window is a run of consecutive loading samples with no
            # settled sample between them.
            runs, current = [], []
            for sample in (vis_log or []):
                if sample.get("loading") == "1":
                    current.append(sample)
                elif current:
                    runs.append(current)
                    current = []
            if current:
                runs.append(current)
            offenders = []
            for run in runs:
                begun = run[0]["at"]
                offenders += [x for x in run
                              if x["at"] > begun + FADE_MS and float(x.get("opacity", 0)) > 0.05]
            if offenders:
                bad(f"the conversation is still visible {FADE_MS}ms into a loading window, so the "
                    f"reader watches it change shape: {offenders}")
            elif settled.get("loading") != "0" or float(settled.get("opacity", 0)) < 0.9:
                bad(f"the conversation was never revealed after the work finished: {settled}")
            else:
                ok(f"the conversation is held at opacity 0 for the whole parse and revealed only "
                   f"when the work is over ({len(runs)} loading window(s), hidden from "
                   f"{hidden[0]['at']}ms, revealed at {settled['at']}ms, allowance {FADE_MS}ms "
                   f"read from the CSS)")

        # --- what changes size AFTER the reveal -------------------------------------
        #
        # The failure the reader still sees: it finishes loading, it looks right, and then something
        # changes the height and the scroll moves. This names the element and the amount, so the
        # cause is measured rather than guessed.
        # Its own computation of the reveal instant: this block sits ABOVE the one that defines
        # `reveal_at`, and reaching for it there raised UnboundLocalError - the probe printed no
        # verdict at all, which the gate reports as a failure with no measurement behind it.
        _loading_ats = [f["at"] for f in (frames or []) if f.get("loading") == "1"]
        _rev_at = None
        if _loading_ats:
            _last = max(_loading_ats)
            for f in (frames or []):
                if (f["at"] > _last and f.get("chat") is not None
                        and float(f["chat"]) > 0.9 and f.get("loading") == "0"):
                    _rev_at = f["at"]
                    break
        if _rev_at is not None:
            after = [x for x in (vis_log or []) if x["at"] >= _rev_at]
            if len(after) >= 2:
                first, last = after[0], after[-1]
                changed = {}
                for k in ("contentHeight", "boxHeight", "clientHeight", "docHeight",
                          "innerHeight", "parentClient"):
                    a, b = first.get(k), last.get(k)
                    if a is not None and b is not None and a != b:
                        changed[k] = f"{a} -> {b}"
                for name in ("parent", "header", "aside"):
                    a, b = first.get(name), last.get(name)
                    if a and b and (a["h"] != b["h"] or a["top"] != b["top"]):
                        changed[name] = f"h {a['h']} -> {b['h']}, top {a['top']} -> {b['top']}"
                await asyncio.to_thread(pathlib.Path(f"{shots}/resize-after-reveal.json").write_text, json.dumps({"first": first, "last": last, "changed": changed}, indent=2))
                if not changed:
                    ok("nothing changes size after the chat becomes readable "
                       "(the conversation was already at its final height)")
                else:
                    bad(f"the layout still changes AFTER the chat is readable: "
                        f"{changed} - the view is re-aimed and the reader sees it move")
            else:
                bad(f"only {len(after)} sample(s) after the reveal: the aftermath was not measured")

        # --- nothing moves the view AFTER the reveal --------------------------------
        #
        # The reader's second report: the chat appears and then "the height readjusts and it ends up
        # wrong". That is a scroll still travelling once the content is visible, and an end-state
        # check cannot see it - the final position is correct either way. Sample the position across
        # the reveal and require it to stop.
        # Only frames AFTER the last loading sample count: the page is legitimately visible before
        # a session starts loading (measured: opacity 1, loading 0), so the first "revealed" frame is
        # the empty shell and measuring from it reported the whole load as movement.
        loading_ats = [f["at"] for f in (frames or []) if f.get("loading") == "1"]
        reveal_at = None
        if loading_ats:
            last_loading = max(loading_ats)
            for f in (frames or []):
                if (f["at"] > last_loading and f.get("chat") is not None
                        and float(f["chat"]) > 0.9 and f.get("loading") == "0"):
                    reveal_at = f["at"]
                    break
        if reveal_at is None:
            bad("the reveal never happened in the recording, so its aftermath was not measured")
        else:
            samples = [f for f in (frames or []) if f["at"] >= reveal_at and f.get("scrollTop") is not None]
            await asyncio.to_thread(pathlib.Path(f"{shots}/scroll-after-reveal.json").write_text, json.dumps(samples, indent=2, ensure_ascii=False))
            positions = [x["scrollTop"] for x in samples]
            if not positions:
                bad("no scroll position was recorded after the reveal")
            else:
                span = max(positions) - min(positions)
                # A scroll animation shows as a sequence of increasing positions after the reveal;
                # an exact landing shows one value, or a couple of pixels of sub-pixel rounding.
                if span <= 2:
                    ok(f"the view is already at rest when the chat becomes readable "
                       f"({span}px of movement across {len(positions)} sample(s), "
                       f"scroll-behavior: {samples[0].get('scrollBehavior')})")
                else:
                    bad(f"the view keeps moving AFTER the chat is revealed ({span}px across "
                        f"{len(positions)} sample(s): {positions[:8]}) - that is the height "
                        f"readjusting the reader reported")

        # --- the modal must LEAVE and the chat must ARRIVE, both gradually -----------
        #
        # Counted from per-frame opacity samples: a transition shows several intermediate values, a
        # jump shows one. The reader reported "it appears very suddenly", which is exactly one frame
        # at the new value, so the assertion counts DISTINCT values strictly between the ends.
        def steps(values):
            seen = []
            for v in values:
                f = float(v)
                if not seen or abs(f - seen[-1]) > 0.01:
                    seen.append(f)
            return seen

        modal_out = steps([x["modal"] for x in (frames or []) if x.get("modal") is not None])
        chat_in = steps([x["chat"] for x in (frames or []) if x.get("chat") is not None])
        intermediate_modal = [v for v in modal_out if 0.02 < v < 0.98]
        intermediate_chat = [v for v in chat_in if 0.02 < v < 0.98]
        await asyncio.to_thread(pathlib.Path(f"{shots}/transitions.json").write_text, json.dumps({"modal": modal_out, "chat": chat_in, "frames": frames}, indent=2, ensure_ascii=False))

        if not frames:
            bad("no per-frame opacity samples were recorded, so nothing about the transitions "
                "was measured")
        else:
            if len(intermediate_modal) >= 2:
                ok(f"the modal fades OUT rather than vanishing ({len(intermediate_modal)} "
                   f"intermediate opacity value(s): "
                   f"{[round(v, 2) for v in intermediate_modal]})")
            else:
                bad(f"the modal disappears in one frame - no fade-out was measured. Opacity "
                    f"sequence: {modal_out}")
            if len(intermediate_chat) >= 2:
                ok(f"the chat fades IN rather than appearing at once "
                   f"({len(intermediate_chat)} intermediate opacity value(s): "
                   f"{[round(v, 2) for v in intermediate_chat]})")
            else:
                bad(f"the chat appears at full opacity in one frame - no fade-in was measured. "
                    f"Opacity sequence: {chat_in}")

        behavior = await c.js("""(() => {
            const el = document.querySelector('main[role="log"]');
            return el ? getComputedStyle(el).scrollBehavior : null;
        })()""")
        if behavior is None:
            bad("the conversation container was not found when measuring its scroll behaviour")
        elif behavior == "smooth":
            bad(f"the conversation container smooth-scrolls (scroll-behavior: {behavior}), so every "
                f"programmatic landing is animated and the next layout change catches it mid-flight")
        else:
            ok(f"the conversation container scrolls exactly (scroll-behavior: {behavior})")

        # And the height it settled at: the hidden phase is what keeps the reader from watching it
        # grow, so the two numbers together are the whole story.
        if vis_log:
            first_h = (vis_log[0].get("contentHeight") or 0)
            last_h = (vis_log[-1].get("contentHeight") or 0)
            if last_h > first_h:
                note(f"the conversation's content grew {first_h}px -> {last_h}px behind the modal, "
                     f"which is exactly what the reader is spared from watching")
            else:
                bad(f"the conversation's content never grew ({first_h}px -> {last_h}px): the "
                    f"hidden phase and the final scroll are then untested")

        ch = await c.js("window.__chatLoadingLog && window.__chatLoadingLog()")
        ml = await c.js("window.__modalLog || []")
        await asyncio.to_thread(pathlib.Path(f"{shots}/chatloading.json").write_text, json.dumps({"state": ch, "modal": ml}, indent=2, ensure_ascii=False))

        # --- the conversation is HIDDEN while it is being built ---------------------
        #
        # Measured on the computed style of the real container, not on a class name: the reader
        # must not watch formulas arrive and diagrams grow, and the scroll must not chase a height
        # that is still changing.
        vis = await c.js("""(() => {
            const el = document.querySelector('main[role="log"]');
            if (!el) return null;
            const cs = getComputedStyle(el);
            const r = el.getBoundingClientRect();
            // opacity 0 on an element is only meaningful if it is really painted, so this also
            // reports what is behind it: the reader sees the modal, not a half-built transcript.
            return { opacity: cs.opacity, loading: el.getAttribute('data-chat-loading'),
                     width: Math.round(r.width), height: Math.round(r.height),
                     modal: !!document.querySelector('.chat-modal-scrim') };
        })()""")
        await asyncio.to_thread(pathlib.Path(f"{shots}/visibility.json").write_text, json.dumps(vis, indent=2, ensure_ascii=False))
        if not vis:
            bad("the conversation container was not found, so nothing about its visibility was measured")
        elif cleared_now:
            if vis.get("loading") == "0" and float(vis.get("opacity", 0)) > 0.9:
                ok(f"the conversation is revealed once the page went quiet "
                   f"(opacity {vis.get('opacity')}, {vis.get('width')}x{vis.get('height')}px)")
            else:
                bad(f"the page went quiet but the conversation is still hidden: {vis}")
        else:
            bad("the conversation was never revealed")

        if not cleared_now:
            bad("the session modal was still up 20s after the transcript settled: the quiet window "
                "never expired, so the reader is left looking at it")
        else:
            ok("the session modal came down, and only after the page went quiet")
            if (ch or {}).get("visiblePending") is False:
                ok(f"the whole visible conversation was built by then "
                   f"({(ch or {}).get('registered', 0)} message(s) registered, nothing visible pending)")
            else:
                bad(f"the modal came down with visible work still outstanding: {ch}")

        puts = [x for x in (ml or []) if x.get("loading")]
        if not puts:
            bad(f"the modal was never put up at all: {ml}")
        elif len(puts) <= 2:
            ok(f"the session modal was put up {len(puts)} time(s) for the whole conversation "
               f"({len(ml or [])} transition(s)): it follows the page going quiet, not each answer")
        else:
            bad(f"the session modal was put up {len(puts)} time(s) — that is the flicker the reader "
                f"reported: loading, finishing, loading again. Transitions: {ml}")

        # And the observer has to be the thing deciding, not a default: it must have reported.
        io = await c.js("window.__ioLog")
        await asyncio.to_thread(pathlib.Path(f"{shots}/io.json").write_text, json.dumps(io, indent=2, ensure_ascii=False))
        if not io or io.get("callbacks", 0) < 1:
            bad(f"the IntersectionObserver never reported: the visibility of a message is "
                f"whatever it was initialised to, not a measurement: {io}")
        else:
            seen_hit = sum(1 for d in io.get("decisions", []) if d.get("hit"))
            seen_miss = sum(1 for d in io.get("decisions", []) if not d.get("hit"))
            ok(f"the intersection observer reported {io['callbacks']} time(s) over "
               f"{len(io.get('decisions', []))} observation(s) — {seen_hit} seen, {seen_miss} not "
               f"seen (rootMargin {((io.get('opts') or {}).get('rootMargin'))})")
            if seen_miss > 0:
                ok(f"the observer really does report both outcomes ({seen_miss} observation(s) "
                   f"outside the margin), so a message is only built on a real measurement")
            else:
                bad("the observer never reported a single observation as outside the viewport: "
                    "the deferral has not been demonstrated, only configured")

        # --- the page follows the deferred work instead of stranding itself ----
        # Formulas, diagrams and emoji images are built after the message is rendered, and
        # each one changes the scroll height. A container that only reacts to state stops
        # wherever that work first left it and the reader has to drag it down by hand.
        #
        # Measured as: how much the container GREW while loading — if the deferred work really
        # does resize the page, that number is large, and it is what makes the check
        # meaningful rather than a tautology about a page that never changed size.
        heights = (ll or {}).get("heights", [])
        grew = (max(heights) - min(heights)) if len(heights) > 1 else 0
        if grew >= 40:
            ok(f"the deferred work resizes the conversation ({min(heights)}px -> {max(heights)}px, "
               f"+{grew}px while loading) - so staying at the bottom is a real requirement")
        else:
            bad(f"the conversation barely changed height while loading ({heights}); the scroll "
                f"check below would prove nothing")
        m2 = await c.js("""(() => {
            const el = document.querySelector('main[role="log"]');
            if (!el) return {error: 'no conversation container'};
            return { atBottom: el.scrollHeight - el.scrollTop - el.clientHeight,
                     scrollHeight: el.scrollHeight, clientHeight: el.clientHeight };
        })()""")
        if "error" in m2:
            bad(m2["error"])
        elif m2["scrollHeight"] > m2["clientHeight"] and m2["atBottom"] <= 2:
            ok(f"the conversation ends at its last line with no scrolling left to do "
               f"({m2['atBottom']}px below the viewport, {m2['scrollHeight']}px of content)")
        else:
            bad(f"the conversation is not scrolled to the bottom: {m2['atBottom']}px of content "
                f"is stranded below the fold ({m2['scrollHeight']}px tall, "
                f"{m2['clientHeight']}px visible)")

    for n in notes:
        print(f"  note  {n}")
    print("\nVERDICT: " + ("the Markdown renders as it should"
                           if not failures else f"FAILED ({len(failures)}) - screenshots and "
                                                f"the measurement are in {shots}"))
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
