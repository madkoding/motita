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
import sys

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
        with open(path, "wb") as f:
            f.write(base64.b64decode(r["data"]))
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
            alt: i.alt, loaded: i.complete && i.naturalWidth > 0};
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
    shots = os.environ.get("SHOTS_DIR", "/tmp/motita-markdown")
    os.makedirs(shots, exist_ok=True)

    # The gateway URL is resolved to a live WebSocket, so this probe never starts a
    # browser of its own: the caller owns that, the same way verify-spinner.sh does.
    import urllib.request

    with urllib.request.urlopen(f"http://127.0.0.1:{cdp_port}/json/list", timeout=5) as r:
        targets = json.load(r)
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
        await c.call("Page.navigate", url=f"{base}/#t={token}")
        await asyncio.sleep(3)

        # The seeded conversation, opened by clicking its row the way a reader would.
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

        # On-demand modules: wait for the diagram to finish rather than guessing.
        for _ in range(40):
            if await c.js("document.querySelectorAll('.mermaid-block svg').length"):
                break
            await asyncio.sleep(0.5)
        await asyncio.sleep(1.5)

        m = await c.js(MEASURE)
        with open(f"{shots}/measured.json", "w") as f:
            json.dump(m, f, indent=2, ensure_ascii=False)
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

    for n in notes:
        print(f"  note  {n}")
    print("\nVERDICT: " + ("the Markdown renders as it should"
                           if not failures else f"FAILED ({len(failures)}) - screenshots and "
                                                f"the measurement are in {shots}"))
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
