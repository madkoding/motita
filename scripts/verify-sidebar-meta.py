"""Measure the motita sidebar: session/project rows and the meta line under each
title, in a real browser against a running gateway.

Run with the venv that has websockets + pillow:
    ~/.hermes/cache/scratch/cdpvenv/bin/python scripts/verify-sidebar-meta.py

What it proves, and why a screenshot alone is not enough: the request was that
the branch, the worktree and the change count sit UNDER the title, "intelligently
and cleanly distributed", with each session showing when it was last updated.
"Under" is a geometry claim and "cleanly distributed" is a claim about overlap
and clipping, so this measures bounding boxes instead of trusting the markup:

  * every meta line's top is BELOW its own title's bottom (it really is under);
  * the title is not truncated away by its metadata (its width is a real share
    of the row, not a sliver);
  * meta chips do not overlap each other;
  * meta chips stay inside the row's right edge (nothing spills out);
  * every session row shows a last-used timestamp;
  * nothing overflows the sidebar horizontally.

It exits non-zero when a claim fails, so it can gate a change.
"""
import asyncio
import base64
import json
import os
import re
import subprocess
import sys
import time
import urllib.request

import websockets

PORT = int(os.environ.get("CDP_PORT", "9341"))
BASE = os.environ.get("GATEWAY_URL", "http://127.0.0.1:7477")
SHOTS = os.environ.get("SHOTS_DIR", "/tmp/motita-sidebar-shots")
CHROME = os.path.expanduser(
    "~/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell"
)


def token():
    with open(os.path.expanduser("~/.motita/gateway.token")) as f:
        return f.read().strip()


class CDP:
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
        r = await self.call("Runtime.evaluate", expression=expr, returnByValue=True, awaitPromise=True)
        if r.get("exceptionDetails"):
            raise RuntimeError(str(r["exceptionDetails"]))
        return r.get("result", {}).get("value")

    async def shot(self, path):
        r = await self.call("Page.captureScreenshot", format="png")
        with open(path, "wb") as f:
            f.write(base64.b64decode(r["data"]))
        return path


# The measurement, run INSIDE the page. It reads the live DOM, so it measures
# what the browser laid out rather than what the source says.
MEASURE = r"""
(() => {
  // A row's layout is: [optional spinner] [container: title, meta] [actions].
  // Reading the TITLE as "the first div inside a div" picks the container
  // instead, whose bottom is below the meta line - which is how an earlier
  // version of this script reported "not under the title" on correct markup.
  // So the container is selected explicitly, then its first two children.
  const parts = (root) => {
    const c = root.querySelector(':scope > div.flex-1');
    if (!c || c.children.length < 2) return null;
    return { title: c.children[0], meta: c.children[1] };
  };

  const box = (el) => {
    const b = el.getBoundingClientRect();
    return { left: b.left, right: b.right, top: b.top, bottom: b.bottom, w: b.width, h: b.height };
  };

  const out = { width: window.innerWidth, sessions: [], projects: [], spill: [] };
  const aside = document.querySelector('aside');
  if (aside) { const b = box(aside); out.sidebar = { left: b.left, right: b.right, w: b.w }; }

  for (const row of [...document.querySelectorAll('.session-row')]) {
    const p = parts(row);
    if (!p) { out.sessions.push({ error: 'no title/meta container' }); continue; }
    const t = box(p.title), m = box(p.meta), r = box(row);
    const chips = [...p.meta.children].map((c) => {
      const b = box(c);
      return {
        text: c.textContent.trim(), title: c.getAttribute('title') || '',
        left: b.left, right: b.right, top: b.top, bottom: b.bottom, w: b.w,
        clipped: c.scrollWidth > c.clientWidth + 1,
        line: Math.round(b.top),
      };
    });
    out.sessions.push({
      titleText: p.title.textContent.trim(),
      selected: row.className.includes('bg-accent/10'),
      titleBottom: t.bottom, titleWidth: t.w,
      metaTop: m.top, metaBottom: m.bottom, metaRight: m.right, metaLeft: m.left,
      rowLeft: r.left, rowRight: r.right,
      chips,
      under: m.top >= t.bottom - 1.5,
      titleNotStarved: t.w >= 60,
      metaInsideRow: m.right <= r.right + 0.5 && m.left >= r.left - 0.5,
    });
  }

  for (const hdr of [...document.querySelectorAll('.project-header')]) {
    const p = parts(hdr);
    if (!p) { out.projects.push({ error: 'no title/meta container' }); continue; }
    const t = box(p.title), m = box(p.meta), r = box(hdr);
    out.projects.push({
      titleText: p.title.textContent.trim(),
      titleBottom: t.bottom, metaTop: m.top, titleWidth: t.w, rowRight: r.right, metaRight: m.right,
      under: m.top >= t.bottom - 1.5,
      titleNotStarved: t.w >= 60,
      metaInsideRow: m.right <= r.right + 0.5,
      chips: [...p.meta.children].map((c) => c.textContent.trim()),
    });
  }

  if (aside) {
    for (const el of aside.querySelectorAll('.session-row *, .project-header *')) {
      const b = box(el);
      if (b.w > 0 && b.right > out.sidebar.right + 1) {
        out.spill.push({ cls: el.className.toString().slice(0, 50), right: b.right, text: el.textContent.trim().slice(0, 30) });
      }
    }
  }
  return out;
})()
"""


def fail(msg):
    print(f"  FAIL {msg}")
    return 1


def main():
    os.makedirs(SHOTS, exist_ok=True)
    failures = 0
    proc = subprocess.Popen(
        [CHROME, f"--remote-debugging-port={PORT}", "--headless", "--no-sandbox",
         "--disable-gpu", "--hide-scrollbars", "about:blank"],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    try:
        # Wait for the DevTools endpoint, and attach to a PAGE target: the
        # browser-level endpoint has no Page domain, so a session against it
        # cannot navigate or screenshot.
        ws_url = None
        for _ in range(60):
            try:
                with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json/list", timeout=1) as r:
                    targets = json.load(r)
                pages = [t for t in targets if t.get("type") == "page"]
                if pages:
                    ws_url = pages[0]["webSocketDebuggerUrl"]
                    break
            except Exception:
                pass
            time.sleep(0.25)
        if not ws_url:
            print("  FAIL could not reach a page target on the DevTools endpoint")
            return 1

        async def run():
            nonlocal failures
            async with websockets.connect(ws_url, max_size=50 * 1024 * 1024) as ws:
                c = CDP(ws)
                await c.call("Page.enable")
                await c.call("Runtime.enable")

                # A narrow viewport is the honest test: the request is about a
                # sidebar that must stay readable when space is tight.
                for label, w, h in [("narrow", 390, 844), ("desktop", 1280, 900)]:
                    await c.call("Emulation.setDeviceMetricsOverride", width=w, height=h,
                                 deviceScaleFactor=1, mobile=False)
                    url = f"{BASE}/#t={token()}"
                    await c.call("Page.navigate", url=url)
                    await asyncio.sleep(2.5)
                    # The service worker can serve a stale bundle; drop it so the
                    # measurement is of the build under test.
                    await c.js("""(async () => {
                        for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
                        for (const k of await caches.keys()) await caches.delete(k);
                    })()""")
                    await c.call("Page.navigate", url=url)
                    await asyncio.sleep(3)

                    m = await c.js(MEASURE)
                    await c.shot(f"{SHOTS}/sidebar-{label}.png")
                    print(f"\n=== {label} ({w}x{h}) ===")
                    print(f"  sidebar: {m.get('sidebar')}")
                    print(f"  sessions measured: {len(m['sessions'])}, projects: {len(m['projects'])}")
                    for s in m["sessions"]:
                        if s.get("error"):
                            failures += fail(f"session row unreadable: {s['error']}")
                            continue
                        print(f"  - title {s['titleText']!r}")
                        print(f"      title bottom={s['titleBottom']:.1f}  meta top={s['metaTop']:.1f}  under={s['under']}")
                        print(f"      title width={s['titleWidth']:.1f} (starved={not s['titleNotStarved']})")
                        for ch in s["chips"]:
                            print(f"      chip {ch['text']!r:42} left={ch['left']:.1f} right={ch['right']:.1f} w={ch['w']:.1f} clipped={ch['clipped']}")
                        if not s["under"]:
                            failures += fail(f"meta is not under the title in {label}: {s['titleText']!r}")
                        if not s["titleNotStarved"]:
                            failures += fail(f"title squeezed to {s['titleWidth']:.1f}px in {label}: {s['titleText']!r}")
                        if not s["metaInsideRow"]:
                            failures += fail(f"meta sticks out of its row in {label}: {s['titleText']!r}")
                        # Chips only collide when they share a line: the meta line
                        # wraps at narrow widths, so comparing a chip with the one
                        # on the next line reports an overlap that is not there.
                        chips = sorted(s["chips"], key=lambda x: (x["line"], x["left"]))
                        for a, b in zip(chips, chips[1:]):
                            if a["line"] == b["line"] and b["left"] < a["right"] - 0.5:
                                failures += fail(f"meta chips overlap in {label}: {a['text']!r} / {b['text']!r}")
                        # Every session shows when it was last updated.
                        if not any(re.search(r"\d\d/\d\d \d\d:\d\d", ch["text"]) for ch in s["chips"]):
                            failures += fail(f"no last-updated timestamp in {label}: {s['titleText']!r}")
                        # An unbroken string of 25 chars can still be clipped by
                        # the max-width, which would hide the value being shown.
                        for ch in s["chips"]:
                            if ch["clipped"]:
                                failures += fail(f"meta chip clipped in {label}: {ch['text']!r} (title {ch['title']!r})")
                        # The id must not be printed twice on one row: the session
                        # branch and its worktree are the same id when the session
                        # has not been moved, and repeating it is noise, not data.
                        ids = [c for c in (ch["text"] for ch in s["chips"]) if re.match(r"^(motita/|⌥ )", c)]
                        if len(ids) != len(set(ids)):
                            failures += fail(f"the same id is printed twice in {label}: {ids}")
                    for p in m["projects"]:
                        if p.get("error"):
                            failures += fail(f"project header unreadable: {p['error']}")
                            continue
                        print(f"  - project {p['titleText']!r}  under={p['under']} title w={p['titleWidth']:.1f} chips={p['chips']}")
                        if not p["under"]:
                            failures += fail(f"project meta is not under the title in {label}: {p['titleText']!r}")
                        if not p["titleNotStarved"]:
                            failures += fail(f"project title squeezed in {label}: {p['titleText']!r}")
                    if m["spill"]:
                        for sp in m["spill"]:
                            failures += fail(f"spills past the sidebar in {label}: {sp}")
        asyncio.run(run())
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except Exception:
            proc.kill()

    print()
    if failures:
        print(f"SIDEBAR VERIFICATION FAILED: {failures} check(s)")
        return 1
    print("SIDEBAR VERIFICATION PASSED: every meta line is under its title, nothing overlaps or spills")
    return 0


if __name__ == "__main__":
    sys.exit(main())
