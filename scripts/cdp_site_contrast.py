"""Measure the ACTUAL contrast of the site's text against its background IMAGE.

Why this exists, and why the normal audit is not enough
-------------------------------------------------------

scripts/audit-page.mjs (and every ancestor-walking contrast checker) finds a
text colour, then walks up the DOM to the first ancestor with an opaque
background and computes the WCAG ratio against THAT. On this site the first
opaque ancestor is ``body { background-color: #0a0a0f }`` - a colour nobody
ever sees, because a fixed illustration is painted over it. The audit therefore
reports a comfortable pass on the wrong pixels, and it cannot be fixed by
raising a threshold: it is looking at the wrong thing.

So this probe measures the pixels a reader actually looks at:

  1. locate the text elements that sit directly on the illustration (not inside
     a frosted panel, which has a background of its own and is measured
     correctly by the ordinary audit),
  2. record each one's colour and geometry,
  3. HIDE the text with ``visibility`` (layout does not move, so the background
     stays exactly where it was) and screenshot,
  4. sample the brightest pixel inside each text box, and compute the real ratio.

The brightest pixel is the honest one: a light patch anywhere under the glyphs
is what makes a line hard to read, and the mean would average it away.

Exit codes: 0 every element meets its ratio · 1 a ratio is below the bar ·
2 the probe cannot run (no browser, no venv) - the convention verify.sh reads as
"skipped" rather than "broken".
"""

import base64
import json
import os
import tempfile
import subprocess
import sys
import time
from collections.abc import Iterable
from typing import Any, NoReturn, Protocol

from cdp_http import JSON, LAUNCHER, SyncSocket, chrome_env

CDP_PORT = os.environ.get("CDP_PORT", "9356")
CHROME = os.environ.get("CHROME", "")
PAGE = os.environ.get("SITE_URL", "")
SHOTS = os.environ.get("SHOTS_DIR") or tempfile.mkdtemp(prefix="motita-site-contrast-")
WIDTH = int(os.environ.get("SITE_WIDTH", "1440"))
HEIGHT = int(os.environ.get("SITE_HEIGHT", "900"))

# The page's own tokens, and the requirement each one carries. WCAG 2.1 AA is
# 4.5:1 for body text; below 18.66px bold or 24px it does not become "large
# text", and the muted colour is used at 13-15px, so 4.5 is the bar for all of
# them. Keeping the requirement next to the colour is what stops a "pass" being
# redefined later to whatever the page happened to measure.
REQUIREMENTS = {
    "rgb(154, 154, 170)": 4.5,   # --muted-fg #9a9aaa
    "rgb(232, 232, 234)": 4.5,   # --fg #e8e8ea
    "rgb(106, 106, 122)": 4.5,   # --faint #6a6a7a
    # The accent is used for the wordmark and the version pill, both of which are
    # large or bold - so the large-text bar (3:1) is the one that applies, not the
    # body-text bar. Declaring it here rather than skipping the colour is the
    # point: an accent that disappears into the illustration is still a defect,
    # and "no declared requirement" would let it pass unmeasured.
    "rgb(76, 194, 255)": 3.0,    # --accent #4cc2ff
}

# The selectors worth measuring: text that sits on the illustration itself.
# Anything inside `.card`, `.stat`, `pre`, `.cmd` or `.diagram` is on a panel
# with its own background, and the ordinary audit measures those correctly.
#
# `.sub` is here because a section subtitle is a direct child of `.wrap`, which
# has no background at all - so it is on the illustration, and it was the case
# this probe first missed entirely.
SELECTORS = ".lede, .hero-note, .hero-title, .pill, .sub, .tab"

failures = []
checked = []


RGB = tuple[int, ...]
Box = tuple[int, int, int, int]


class PixelImage(Protocol):
    """The part of a PIL image this probe uses; PIL is only imported once the venv is checked."""

    def convert(self, mode: str) -> "PixelImage":
        """The image in another pixel mode."""

    def crop(self, box: Box) -> "PixelImage":
        """The part of the image inside box."""

    def getdata(self) -> Iterable[RGB]:
        """Every pixel, row by row."""


class ImageOpener(Protocol):
    """PIL's Image module, as far as this probe uses it."""

    def open(self, fp: str) -> PixelImage:
        """Read the image file at fp."""


def die(code: int, message: str) -> NoReturn:
    """Print message and exit with code."""
    print(message)
    sys.exit(code)


def load_deps() -> tuple[Any, Any]:
    """The websockets sync client and PIL's Image, or a SKIP exit when the venv lacks them."""
    try:
        import websockets.sync.client as ws_client
    except ImportError as exc:  # pragma: no cover - environment, not logic
        die(2, f"SKIP: the probe venv is missing websockets ({exc})")

    try:
        from PIL import Image
    except ImportError as exc:  # pragma: no cover
        die(2, f"SKIP: the probe venv is missing pillow ({exc})")
    return ws_client, Image


def first_page_target() -> str | None:
    """The websocket URL of the browser's first page target, or None (also on a bad read)."""
    try:
        listing = json.loads(
            subprocess.run(
                ["curl", "-sf", f"http://127.0.0.1:{CDP_PORT}/json/list"],
                capture_output=True,
                text=True,
                timeout=5,
            ).stdout
            or "[]"
        )
        for target in listing:
            if target.get("type") == "page":
                return target.get("webSocketDebuggerUrl")
    except (subprocess.SubprocessError, json.JSONDecodeError):
        pass
    return None


def wait_page_target() -> str | None:
    """Poll /json/list for up to 20 s until the browser offers a page target."""
    # The CDP websocket is NOT at a fixed path: the browser names it per target,
    # so it has to be read out of /json/list. Hardcoding /devtools/page/ connects
    # to nothing and the probe dies with a timeout that looks like a missing
    # browser rather than a wrong URL.
    target_url = None
    deadline = time.time() + 20
    while time.time() < deadline and target_url is None:
        target_url = first_page_target()
        if target_url is None:
            time.sleep(0.25)
    return target_url


def main() -> None:
    """Start the browser, measure the page and exit with the verdict's code."""
    if not CHROME or not os.path.exists(CHROME):
        die(2, "SKIP: no chrome-headless-shell (set CHROME=...)")
    if not PAGE:
        die(2, "SKIP: set SITE_URL to the page to measure")

    ws_client, image_cls = load_deps()

    os.makedirs(SHOTS, exist_ok=True)

    chrome = subprocess.Popen(
        ["sh", LAUNCHER, "--headless=new", "--no-sandbox", "--disable-gpu", "about:blank"],
        env=chrome_env(CHROME, CDP_PORT, profile=os.path.join(SHOTS, "cdp-profile"),
                       window=f"{WIDTH},{HEIGHT}"),
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )

    target_url = wait_page_target()

    if target_url is None:
        chrome.terminate()
        die(2, "SKIP: the browser never offered a page target over CDP")

    ws = ws_client.connect(target_url, max_size=200 * 1024 * 1024)
    try:
        run(ws, image_cls)
    finally:
        try:
            ws.close()
        except Exception:
            pass
        chrome.terminate()


class Browser:
    """Synchronous CDP calls over one websocket."""

    def __init__(self, ws: SyncSocket) -> None:
        self.ws = ws
        self.n = 0

    def call(self, method: str, **params: Any) -> dict[str, Any]:
        """Send one CDP command and wait for its own reply."""
        self.n += 1
        msg_id = self.n
        self.ws.send(json.dumps({"id": msg_id, "method": method, "params": params}))
        while True:
            reply = json.loads(self.ws.recv())
            if reply.get("id") == msg_id:
                if "error" in reply:
                    raise RuntimeError(f"{method}: {reply['error']}")
                return reply.get("result", {})

    def js(self, expr: str) -> JSON:
        """Evaluate expr in the page and return its value."""
        result = self.call("Runtime.evaluate", expression=expr, returnByValue=True)
        value = result.get("result", {}).get("value")
        if value is None and "exceptionDetails" in result:
            raise RuntimeError(f"eval failed: {result['exceptionDetails']}")
        return value

    # Hide EVERY child of body, and restore in one place. Hiding only the
    # measured elements is not enough: the screenshot showed headings and stat
    # text still painted, and a heading inside a text box's rectangle is what a
    # sample picks up - the probe then reports the TEXT's own colour as the
    # background and calls a good page unreadable.
    #
    # `visibility` rather than `display`, so nothing reflows: the background is
    # `position: fixed` and the boxes keep the geometry they were measured with.
    # body's ::before pseudo-element is not a child, so the illustration stays.
    def hide_content(self, hidden: bool) -> None:
        """Hide (or show again) every child of body, leaving only the illustration painted."""
        self.js(
            """(() => {
                 for (const child of document.body.children) {
                   child.style.visibility = %s;
                 }
                 return true;
               })()"""
            % ("'hidden'" if hidden else "''")
        )

    def screenshot(self, path: str) -> None:
        """Write a PNG of the viewport to path."""
        shot = self.call("Page.captureScreenshot", format="png")
        with open(path, "wb") as handle:
            handle.write(base64.b64decode(shot["data"]))


def open_page(b: Browser) -> None:
    """Load PAGE with the cache off and check the window has the width asked for."""
    b.call("Page.enable")
    b.call("Runtime.enable")
    # The profile is reused between runs, so without this the PREVIOUS page is
    # what gets measured - and a fix and a stale cache then look identical.
    b.call("Network.enable")
    b.call("Network.setCacheDisabled", cacheDisabled=True)

    b.call("Page.navigate", url=PAGE)
    time.sleep(2.5)

    if b.js("window.innerWidth") != WIDTH:
        die(1, f"FAIL: the window is {b.js('window.innerWidth')}px wide, not {WIDTH}; "
               "an emulated width leaves a wider layout viewport behind and the "
               "measurement would be of the wrong page")


def list_elements(b: Browser) -> list[dict[str, Any]]:
    """Tag every element matching SELECTORS with its index and return its colour and text."""
    return json.loads(
        b.js(
            """JSON.stringify([...document.querySelectorAll(%s)].map((el, i) => {
                 const r = el.getBoundingClientRect();
                 const cs = getComputedStyle(el);
                 el.dataset.contrastIndex = String(i);
                 return {
                   i: i,
                   tag: el.tagName,
                   cls: el.className || el.tagName,
                   color: cs.color,
                   fontSize: parseFloat(cs.fontSize),
                   weight: cs.fontWeight,
                   text: (el.textContent || '').trim().slice(0, 60),
                 };
               }).filter(e => e.i >= 0))"""
            % json.dumps(SELECTORS)
        )
    )


def locate(b: Browser, el: dict[str, Any]) -> dict[str, int] | None:
    """Scroll el to the middle of the viewport and return its box, or None once it is gone."""
    # The background is `position: fixed`, so the pixels behind a section
    # depend on the SCROLL OFFSET - measuring the whole page from one
    # screenshot would sample the top of the illustration for every element.
    # Each one is scrolled to the middle of the viewport and measured there.
    #
    # The index is carried on the element itself: looking it up by comparing
    # dicts in Python finds the first EQUAL one, and two elements of the same
    # class with the same colour are equal - which silently measured the
    # wrong box.
    selector = '[data-contrast-index="%d"]' % el["i"]
    b.js(
        """(() => {
             const el = document.querySelector(%s);
             if (!el) return false;
             el.scrollIntoView({block: 'center'});
             return true;
           })()"""
        % json.dumps(selector)
    )
    time.sleep(0.25)
    return json.loads(
        b.js(
            """(() => {
                 const el = document.querySelector(%s);
                 if (!el) return null;
                 const r = el.getBoundingClientRect();
                 return JSON.stringify({
                   x: Math.round(r.x), y: Math.round(r.y),
                   w: Math.round(r.width), h: Math.round(r.height),
                 });
               })()"""
            % json.dumps(selector)
        )
        or "null"
    )


def measure(b: Browser, image_cls: ImageOpener, el: dict[str, Any]) -> None:
    """Measure one element's text colour against the brightest pixel behind it."""
    required = REQUIREMENTS.get(el["color"])
    if required is None:
        print(f"  skip  {el['cls'][:34]:34} colour {el['color']} has no declared requirement")
        return

    rect = locate(b, el)
    if not rect:
        print(f"  skip  {el['cls'][:34]:34} is no longer in the DOM")
        return

    b.hide_content(True)
    time.sleep(0.2)
    bg_path = os.path.join(SHOTS, f"bg-{el['i']}-{el['cls'].split()[0]}.png")
    b.screenshot(bg_path)
    b.hide_content(False)
    image = image_cls.open(bg_path).convert("RGB")

    box = (
        max(0, rect["x"]),
        max(0, rect["y"]),
        min(image.width, rect["x"] + rect["w"]),
        min(image.height, rect["y"] + rect["h"]),
    )
    if box[2] <= box[0] or box[3] <= box[1]:
        print(f"  skip  {el['cls'][:34]:34} could not be brought into the viewport")
        return

    brightest = brightest_pixel(image, box)
    ratio = contrast_ratio(parse_rgb(el["color"]), brightest)
    verdict = "ok   " if ratio >= required else "FAIL "
    checked.append(ratio)
    if ratio < required:
        failures.append((el, ratio, required, brightest))

    print(
        f"  {verdict} {el['cls'][:30]:30} {ratio:5.2f}:1 (needs {required}) "
        f"over {fmt_rgb(brightest)}  \"{el['text'][:40]}\""
    )


def run(ws: SyncSocket, image_cls: ImageOpener) -> None:
    """Measure every selected element on the page behind ws and print the verdict."""
    b = Browser(ws)
    open_page(b)

    elements = list_elements(b)
    if not elements:
        die(1, f"FAIL: no element matched {SELECTORS!r}; this probe is not looking at the page")

    print(f"== measuring {len(elements)} element(s) at {WIDTH}x{HEIGHT}")

    for el in elements:
        measure(b, image_cls, el)

    # Put the page back, so any screenshot left for a human shows the real thing.
    b.hide_content(False)
    b.screenshot(os.path.join(SHOTS, f"page-{WIDTH}.png"))

    print()
    if not checked:
        die(1, "FAIL: nothing was measured, so nothing was verified")
    worst = min(checked)
    print(f"worst ratio measured: {worst:.2f}:1 over the illustrated background")
    print(f"screenshots: {SHOTS}")
    if failures:
        print(f"VERDICT: FAILED - {len(failures)} element(s) below their requirement")
        sys.exit(1)
    print("VERDICT: every measured element meets its ratio against the real pixels")


def parse_rgb(value: str) -> RGB:
    """The (r, g, b) of a CSS rgb()/rgba() string, comma or space separated."""
    inner = value[value.index("(") + 1 : value.index(")")]
    # Comma-separated is what getComputedStyle returns; space/slash is the modern
    # form. Both appear in this project's stylesheets, so both are accepted.
    cleaned = inner.replace(",", " ").replace("/", " ")
    parts = [float(p) for p in cleaned.split()[:3]]
    return tuple(int(round(p)) for p in parts)


def fmt_rgb(rgb: RGB) -> str:
    """rgb as a #rrggbb hex string."""
    return "#%02x%02x%02x" % rgb


def brightest_pixel(image: PixelImage, box: Box) -> RGB:
    """The brightest pixel in the box - the one that decides legibility."""
    region = image.crop(box)
    pixels = list(region.getdata())
    return max(pixels, key=relative_luminance)


def relative_luminance(rgb: RGB) -> float:
    """WCAG 2.1 relative luminance of an sRGB colour."""
    def channel(value: float) -> float:
        """One sRGB channel, 0-255, linearised."""
        value = value / 255
        return value / 12.92 if value <= 0.03928 else ((value + 0.055) / 1.055) ** 2.4

    red, green, blue = rgb
    return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue)


def contrast_ratio(first: RGB, second: RGB) -> float:
    """WCAG contrast ratio between two colours, always >= 1."""
    a, b = relative_luminance(first), relative_luminance(second)
    lighter, darker = max(a, b), min(a, b)
    return (lighter + 0.05) / (darker + 0.05)


if __name__ == "__main__":
    main()
