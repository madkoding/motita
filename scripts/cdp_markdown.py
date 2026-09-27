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

```mermaid
graph LR
  A[Inicio] --> B[Fin]
```

```js
const arrow = a --> b
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

  // 7. DIAGRAM: drawn, with visible LABELS. `nodeLabel` text is the whole point: the
  //    SVG renders two empty boxes without it, which every shape-based check passes.
  const mm = box.querySelector('.mermaid-block');
  const svg = mm ? mm.querySelector('svg') : null;
  const svgRect = svg ? svg.getBoundingClientRect() : null;
  const labels = [...(mm ? mm.querySelectorAll('.nodeLabel') : [])]
    .map(n => n.textContent.trim()).filter(Boolean);
  const labelVisible = [...(mm ? mm.querySelectorAll('.nodeLabel') : [])].some(n => {
    const r = n.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  });
  const edges = mm ? mm.querySelectorAll('.edgePaths path, path.flowchart-link').length : 0;
  const arrowMarker = mm
    ? [...mm.querySelectorAll('path')].some(p => p.getAttribute('marker-end'))
    : false;

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

  return {
    lineBreak: {br: brCount, paragraphHeight: p1Height},
    escape: {text: escapeText, emInside: escapeHasEm},
    inlineHtml: {kbd: q('kbd'), mark: q('mark'), sub: q('sub'), sup: q('sup'), markBg},
    footnotes: {refs: q('a[href^="#fn"]'), targetExists: !!fnTarget, listed: fnListed},
    deflist: {dl: !!dl, dt, dd},
    math: {count: maths.length, geom: mathGeom,
           blockVisible: blockMath ? blockMath.getBoundingClientRect().height > 0 : false,
           rawTexShowing: rawTex, annotationLeak},
    mermaid: {present: !!mm, svg: !!svg,
              w: svgRect ? Math.round(svgRect.width) : 0,
              h: svgRect ? Math.round(svgRect.height) : 0,
              labels, labelVisible, edges, arrowMarker,
              done: mm ? mm.getAttribute('data-done') : null},
    alerts,
    code: {copyTextLen: copyText === null ? null : copyText.length,
           copyText, tokenColor,
           codeLen: codeText ? codeText.textContent.length : 0},
    messageCopyLen: msgRaw.length,
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
        if math["count"] >= 2 and math["blockVisible"]:
            ok(f"both formulas are typeset as MathML ({math['count']} <math>, "
               f"block height > 0): {math['geom']}")
        else:
            bad(f"the formulas were not typeset: {math}")
        if math["rawTexShowing"] or math["annotationLeak"]:
            bad(f"the RAW LaTeX is still visible ({math['rawTexShowing']} / "
                f"annotation text inside <math>: {math['annotationLeak']}) — the "
                f"accessible annotation was dropped but its text was left behind")
        else:
            ok("no raw LaTeX is printed beside the typeset formula")

        # --- diagram ----------------------------------------------------------
        mm = m["mermaid"]
        if not (mm["present"] and mm["svg"]):
            bad("the diagram was not drawn at all")
        elif mm["w"] == 0 or mm["h"] == 0:
            bad(f"the diagram has no size ({mm['w']}x{mm['h']})")
        else:
            ok(f"the diagram is drawn ({mm['w']}x{mm['h']}, {mm['edges']} edge, "
               f"arrow marker: {mm['arrowMarker']})")
        if mm["labels"] == ["Inicio", "Fin"] and mm["labelVisible"]:
            ok(f"the diagram's node labels are drawn and visible: {mm['labels']}")
        else:
            bad(f"the diagram's node labels are missing or invisible: {mm['labels']} "
                f"(boxes, edges and markers can all render with no text in them)")

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

        # --- nothing failed to render ----------------------------------------
        if m["renderErrors"] == 0:
            ok("no element reported a render failure")
        else:
            bad(f"{m['renderErrors']} element(s) rendered as an error box")

    for n in notes:
        print(f"  note  {n}")
    print("\nVERDICT: " + ("the Markdown renders as it should"
                           if not failures else f"FAILED ({len(failures)}) - screenshots and "
                                                f"the measurement are in {shots}"))
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
