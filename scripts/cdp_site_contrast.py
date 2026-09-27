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
import subprocess
import sys
import time

CDP_PORT = os.environ.get("CDP_PORT", "9356")
CHROME = os.environ.get("CHROME", "")
PAGE = os.environ.get("SITE_URL", "")
SHOTS = os.environ.get("SHOTS_DIR", "/tmp/motita-site-contrast")
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


def die(code, message):
    print(message)
    sys.exit(code)


def main():
    if not CHROME or not os.path.exists(CHROME):
        die(2, "SKIP: no chrome-headless-shell (set CHROME=...)")
    if not PAGE:
        die(2, "SKIP: set SITE_URL to the page to measure")

    try:
        import websockets.sync.client as ws_client
    except ImportError as exc:  # pragma: no cover - environment, not logic
        die(2, f"SKIP: the probe venv is missing websockets ({exc})")

    try:
        from PIL import Image
    except ImportError as exc:  # pragma: no cover
        die(2, f"SKIP: the probe venv is missing pillow ({exc})")

    os.makedirs(SHOTS, exist_ok=True)

    chrome = subprocess.Popen(
        [
            CHROME,
            "--headless=new",
            f"--remote-debugging-port={CDP_PORT}",
            f"--user-data-dir={SHOTS}/cdp-profile",
            "--no-sandbox",
            "--disable-gpu",
            f"--window-size={WIDTH},{HEIGHT}",
            "about:blank",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )

    # The CDP websocket is NOT at a fixed path: the browser names it per target,
    # so it has to be read out of /json/list. Hardcoding /devtools/page/ connects
    # to nothing and the probe dies with a timeout that looks like a missing
    # browser rather than a wrong URL.
    target_url = None
    deadline = time.time() + 20
    while time.time() < deadline and target_url is None:
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
                    target_url = target.get("webSocketDebuggerUrl")
                    break
        except (subprocess.SubprocessError, json.JSONDecodeError):
            pass
        if target_url is None:
            time.sleep(0.25)

    if target_url is None:
        chrome.terminate()
        die(2, "SKIP: the browser never offered a page target over CDP")

    ws = ws_client.connect(target_url, max_size=200 * 1024 * 1024)
    try:
        run(ws, Image)
    finally:
        try:
            ws.close()
        except Exception:
            pass
        chrome.terminate()


def run(ws, Image):
    counter = {"n": 0}

    def call(method, **params):
        counter["n"] += 1
        msg_id = counter["n"]
        ws.send(json.dumps({"id": msg_id, "method": method, "params": params}))
        while True:
            reply = json.loads(ws.recv())
            if reply.get("id") == msg_id:
                if "error" in reply:
                    raise RuntimeError(f"{method}: {reply['error']}")
                return reply.get("result", {})

    def js(expr):
        result = call("Runtime.evaluate", expression=expr, returnByValue=True)
        value = result.get("result", {}).get("value")
        if value is None and "exceptionDetails" in result:
            raise RuntimeError(f"eval failed: {result['exceptionDetails']}")
        return value

    call("Page.enable")
    call("Runtime.enable")
    # The profile is reused between runs, so without this the PREVIOUS page is
    # what gets measured - and a fix and a stale cache then look identical.
    call("Network.enable")
    call("Network.setCacheDisabled", cacheDisabled=True)

    call("Page.navigate", url=PAGE)
    time.sleep(2.5)

    if js("window.innerWidth") != WIDTH:
        die(1, f"FAIL: the window is {js('window.innerWidth')}px wide, not {WIDTH}; "
               "an emulated width leaves a wider layout viewport behind and the "
               "measurement would be of the wrong page")

    elements = json.loads(
        js(
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
    if not elements:
        die(1, f"FAIL: no element matched {SELECTORS!r}; this probe is not looking at the page")

    print(f"== measuring {len(elements)} element(s) at {WIDTH}x{HEIGHT}")

    # Hide EVERY child of body, and restore in one place. Hiding only the
    # measured elements is not enough: the screenshot showed headings and stat
    # text still painted, and a heading inside a text box's rectangle is what a
    # sample picks up - the probe then reports the TEXT's own colour as the
    # background and calls a good page unreadable.
    #
    # `visibility` rather than `display`, so nothing reflows: the background is
    # `position: fixed` and the boxes keep the geometry they were measured with.
    # body's ::before pseudo-element is not a child, so the illustration stays.
    def hide_content(hidden):
        js(
            """(() => {
                 for (const child of document.body.children) {
                   child.style.visibility = %s;
                 }
                 return true;
               })()"""
            % ("'hidden'" if hidden else "''")
        )

    image_cls = Image

    for el in elements:
        required = REQUIREMENTS.get(el["color"])
        if required is None:
            print(f"  skip  {el['cls'][:34]:34} colour {el['color']} has no declared requirement")
            continue

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
        js(
            """(() => {
                 const el = document.querySelector(%s);
                 if (!el) return false;
                 el.scrollIntoView({block: 'center'});
                 return true;
               })()"""
            % json.dumps(selector)
        )
        time.sleep(0.25)
        rect = json.loads(
            js(
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
        if not rect:
            print(f"  skip  {el['cls'][:34]:34} is no longer in the DOM")
            continue

        hide_content(True)
        time.sleep(0.2)
        shot = call("Page.captureScreenshot", format="png")
        bg_path = os.path.join(SHOTS, f"bg-{el['i']}-{el['cls'].split()[0]}.png")
        with open(bg_path, "wb") as handle:
            handle.write(base64.b64decode(shot["data"]))
        hide_content(False)
        image = image_cls.open(bg_path).convert("RGB")

        box = (
            max(0, rect["x"]),
            max(0, rect["y"]),
            min(image.width, rect["x"] + rect["w"]),
            min(image.height, rect["y"] + rect["h"]),
        )
        if box[2] <= box[0] or box[3] <= box[1]:
            print(f"  skip  {el['cls'][:34]:34} could not be brought into the viewport")
            continue

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

    # Put the page back, so any screenshot left for a human shows the real thing.
    hide_content(False)
    lit = call("Page.captureScreenshot", format="png")
    with open(os.path.join(SHOTS, f"page-{WIDTH}.png"), "wb") as handle:
        handle.write(base64.b64decode(lit["data"]))

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


def parse_rgb(value):
    inner = value[value.index("(") + 1 : value.index(")")]
    # Comma-separated is what getComputedStyle returns; space/slash is the modern
    # form. Both appear in this project's stylesheets, so both are accepted.
    cleaned = inner.replace(",", " ").replace("/", " ")
    parts = [float(p) for p in cleaned.split()[:3]]
    return tuple(int(round(p)) for p in parts)


def fmt_rgb(rgb):
    return "#%02x%02x%02x" % rgb


def brightest_pixel(image, box):
    """The brightest pixel in the box - the one that decides legibility."""
    region = image.crop(box)
    pixels = list(region.getdata())
    return max(pixels, key=relative_luminance)


def relative_luminance(rgb):
    def channel(value):
        value = value / 255
        return value / 12.92 if value <= 0.03928 else ((value + 0.055) / 1.055) ** 2.4

    red, green, blue = rgb
    return 0.2126 * channel(red) + 0.7152 * channel(green) + 0.0722 * channel(blue)


def contrast_ratio(first, second):
    a, b = relative_luminance(first), relative_luminance(second)
    lighter, darker = max(a, b), min(a, b)
    return (lighter + 0.05) / (darker + 0.05)


if __name__ == "__main__":
    main()
