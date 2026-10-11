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
from typing import Any

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

Emoji: caras 🙂 🥳 🚀 ❤️ 🔥, bandera arcoiris 🏳️‍🌈, gato negro 🐈‍⬛, \
teclas 1️⃣ 2️⃣, una sin asset 🇨🇱 y otra sin asset 🏴󠁧󠁢󠁳󠁣󠁴󠁿.

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


# The page-side scripts and the checks live in their own modules. The probe's directory is
# put on the path first because verify-markdown.sh loads this file by path to seed the
# session from EXAMPLE.
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from cdp_markdown_js import LOAD_LOG, MEASURE, RECORDER  # noqa: E402
from cdp_markdown_checks import (  # noqa: E402
    bad, check_alerts, check_blocks, check_code, check_diagrams, check_emoji, check_loading,
    check_render_errors, check_text, failures, notes, ok, save_json,
)

PAGE_NAVIGATE = "Page.navigate"

class CDP:
    """A CDP client with exactly ONE coroutine reading the socket.

    Two readers raise `cannot call recv while another coroutine is already running
    recv`, and a collector task alongside the request loop is the usual way to write
    that bug.
    """

    def __init__(self, ws: Any) -> None:
        self.ws = ws
        self.i = 0

    async def call(self, method: str, **params: Any) -> dict[str, Any]:
        """Send one CDP command and wait for its own reply."""
        self.i += 1
        mid = self.i
        await self.ws.send(json.dumps({"id": mid, "method": method, "params": params}))
        while True:
            msg = json.loads(await self.ws.recv())
            if msg.get("id") == mid:
                if "error" in msg:
                    raise RuntimeError(f"{method}: {msg['error']}")
                return msg.get("result", {})

    async def js(self, expr: str) -> Any:
        """Evaluate expr in the page (awaiting a promise) and return its value."""
        r = await self.call(
            "Runtime.evaluate", expression=expr, returnByValue=True, awaitPromise=True
        )
        if r.get("exceptionDetails"):
            raise RuntimeError(str(r["exceptionDetails"])[:900])
        return r.get("result", {}).get("value")

    async def shot(self, path: str) -> str:
        """Save a PNG screenshot of the page to path."""
        r = await self.call("Page.captureScreenshot", format="png")
        await asyncio.to_thread(pathlib.Path(path).write_bytes, base64.b64decode(r["data"]))
        return path


def main() -> int:
    """Attach to the browser's page and run the probe; returns the exit code."""
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


async def open_app(c: CDP, base: str, token: str) -> None:
    """Load the app fresh (no cache, no service worker) with the page-side recorders installed."""
    await c.call("Page.enable")
    await c.call("Runtime.enable")
    await c.call("Network.enable")
    # The service worker serves the OLD shell. Without this, every measurement
    # describes the previous build and reads as "my change did nothing".
    await c.call("Network.setCacheDisabled", cacheDisabled=True)

    # about:blank FIRST: a navigation that differs only in the FRAGMENT does not
    # reload, so going straight to the token URL twice leaves the first page in place.
    await c.call(PAGE_NAVIGATE, url="about:blank")
    await c.call(PAGE_NAVIGATE, url=f"{base}/#t={token}")
    await asyncio.sleep(2.5)
    await c.js("""(async () => {
        for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
        for (const k of await caches.keys()) await caches.delete(k);
    })()""")
    await c.call(PAGE_NAVIGATE, url="about:blank")
    # A recorder that outlives the navigation it observes: it watches the conversation container
    # from the first frame, so "was it hidden WHILE loading?" is answered from the page's own
    # history instead of being inferred from a sample taken later.
    await c.call("Page.addScriptToEvaluateOnNewDocument", source=RECORDER)
    await c.call(PAGE_NAVIGATE, url=f"{base}/#t={token}")
    await asyncio.sleep(3)

    # The seeded conversation, opened by clicking its row the way a reader would.
    #
    # The observation is installed BEFORE the click, and that is the only place it can be:
    # the spinner exists exactly while the conversation is being put on screen, so a check
    # that looks for it afterwards is looking for something that has deliberately gone.
    await c.js(LOAD_LOG)


async def wait_answer(c: CDP) -> bool:
    """Wait up to ~12 s for the seeded answer to be rendered."""
    for _ in range(24):
        await asyncio.sleep(0.5)
        if await c.js("""(() => [...document.querySelectorAll('.markdown-body')]
              .some(b => b.textContent.includes('Salto')))()"""):
            return True
    return False


async def open_conversation(c: CDP) -> bool:
    """Click the seeded conversation's row until its answer is on screen; False when it never is."""
    for _ in range(8):
        rows = await c.js("""(() => [...document.querySelectorAll('.session-row')].map((r, i) => ({
            i, title: (r.querySelector(':scope > div.flex-1 > div') || {}).textContent?.trim() || ''
        })))()""")
        target = next((r["i"] for r in rows if "Markdown" in r["title"]), None)
        if target is None:
            return False
        await c.js(f"document.querySelectorAll('.session-row')[{target}].click()")
        if await wait_answer(c):
            return True
    return False


async def scroll_to_answer(c: CDP) -> None:
    """Scroll to the last turn, which starts its hydration, and wait for its diagram."""
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


async def run(ws_url: str, base: str, token: str, shots: str) -> int:
    """Open the seeded conversation and measure how it renders; returns the exit code."""
    import websockets

    async with websockets.connect(ws_url, max_size=80 * 1024 * 1024) as ws:
        c = CDP(ws)
        await open_app(c, base, token)

        opened = await open_conversation(c)
        if not opened:
            bad("the seeded conversation could not be opened in the interface")
            await c.shot(f"{shots}/not-opened.png")
            print("\nVERDICT: FAILED (the page was never reached)")
            return 1
        ok("the seeded answer is rendered in the interface")
        await scroll_to_answer(c)

        m = await c.js(MEASURE)
        await save_json(f"{shots}/measured.json", m)
        await c.shot(f"{shots}/rendered.png")

        if "error" in m:
            bad(m["error"])
            print("\nVERDICT: FAILED (nothing measured)")
            return 1

        check_text(m)
        check_blocks(m)
        await check_diagrams(c, m)
        check_emoji(m["emoji"])
        check_alerts(m)
        check_code(m)
        check_render_errors(m)
        await check_loading(c, shots)

    for n in notes:
        print(f"  note  {n}")
    print("\nVERDICT: " + ("the Markdown renders as it should"
                           if not failures else f"FAILED ({len(failures)}) - screenshots and "
                                                f"the measurement are in {shots}"))
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(main())
