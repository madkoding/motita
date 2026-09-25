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
# A throwaway gateway runs under an isolated HOME, so the token and the browser
# are looked up through the environment rather than assumed to be in the real
# home: expanduser would otherwise reach the wrong tree (or the wrong file).
TOKEN_FILE = os.environ.get("MOTITA_TOKEN_FILE", "~/.motita/gateway.token")
CHROME = os.path.expanduser(
    os.environ.get(
        "CDP_CHROME",
        "~/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell",
    )
)


def token():
    with open(os.path.expanduser(TOKEN_FILE)) as f:
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
  // A row's layout is: [optional spinner] [container: title, timestamp, facts]
  // [actions]. Reading the TITLE as "the first div inside a div" picks the
  // container instead, whose bottom is below the facts line - which is how an
  // earlier version of this script reported "not under the title" on correct
  // markup. So the container is selected explicitly, then its children: the
  // first is the title, the second the timestamp, the third the labelled facts.
  const parts = (root) => {
    const c = root.querySelector(':scope > div.flex-1');
    if (!c || c.children.length < 2) return null;
    return {
      title: c.children[0],
      stamp: c.children[1],
      meta: c.children.length > 2 ? c.children[2] : c.children[1],
    };
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
    const t = box(p.title), m = box(p.meta), r = box(row), st = box(p.stamp);
    const chips = [...p.meta.children].map((c) => {
      const b = box(c);
      // A chip is [label][value] when it carries a label; the label is the dim
      // one and the value the bright one. Both are reported so the checks can
      // assert that every fact NAMES itself and that each kind has its own
      // colour, rather than trusting the markup.
      const kids = [...c.children];
      const label = kids.length > 1 ? kids[0].textContent.trim() : '';
      const value = kids.length > 1 ? kids[1] : (kids[0] || c);
      return {
        text: c.textContent.trim(), title: c.getAttribute('title') || '',
        label, valueText: value.textContent.trim(),
        valueCls: value.className.toString(),
        left: b.left, right: b.right, top: b.top, bottom: b.bottom, w: b.w,
        clipped: c.scrollWidth > c.clientWidth + 1,
        line: Math.round(b.top),
        cls: c.className.toString(),
      };
    });
    out.sessions.push({
      titleText: p.title.textContent.trim(),
      stampText: p.stamp.textContent.trim(),
      selected: row.className.includes('bg-accent/10'),
      titleBottom: t.bottom, titleWidth: t.w,
      stampTop: st.top, stampLeft: st.left, stampRight: st.right,
      metaTop: m.top, metaBottom: m.bottom, metaRight: m.right, metaLeft: m.left,
      rowLeft: r.left, rowRight: r.right,
      chips,
      // The timestamp is its own line now, so "under the title" is measured for
      // the stamp as well as for the facts.
      under: m.top >= t.bottom - 1.5,
      stampUnder: st.top >= t.bottom - 1.5,
      stampAboveFacts: st.bottom <= m.top + 1.5,
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
      chips: [...p.meta.children].map((c) => {
        const kids = [...c.children];
        const label = kids.length > 1 ? kids[0].textContent.trim() : '';
        const value = kids.length > 1 ? kids[1] : (kids[0] || c);
        return {
          text: c.textContent.trim(), label,
          valueText: value.textContent.trim(),
          valueCls: value.className.toString(),
        };
      }),
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
                        print(f"      title bottom={s['titleBottom']:.1f}  stamp top={s['stampTop']:.1f}  facts top={s['metaTop']:.1f}")
                        print(f"      stamp {s['stampText']!r}  under={s['stampUnder']}  aboveFacts={s['stampAboveFacts']}")
                        print(f"      title width={s['titleWidth']:.1f} (starved={not s['titleNotStarved']})")
                        for ch in s["chips"]:
                            print(f"      chip {ch['text']!r:46} w={ch['w']:.1f} clipped={ch['clipped']}")
                        if not s["under"]:
                            failures += fail(f"facts are not under the title in {label}: {s['titleText']!r}")
                        # The timestamp must be on its own line, BETWEEN the
                        # title and the facts - that is the break the user asked
                        # for. Sharing the facts line is the old behaviour.
                        if not s["stampUnder"] or not s["stampAboveFacts"]:
                            failures += fail(f"the timestamp is not on its own line under the title in {label}: {s['titleText']!r}")
                        # The timestamp LINE is what must carry the format, not a
                        # chip: it moved out of the facts row deliberately.
                        if not re.search(r"\d\d/\d\d/\d{4} :: \d\d:\d\d:\d\d", s["stampText"]):
                            failures += fail(f"the timestamp line is not dd/mm/yyyy :: HH:mm:ss in {label}: {s['stampText']!r}")
                        if not s["titleNotStarved"]:
                            failures += fail(f"title squeezed to {s['titleWidth']:.1f}px in {label}: {s['titleText']!r}")
                        if not s["metaInsideRow"]:
                            failures += fail(f"meta sticks out of its row in {label}: {s['titleText']!r}")
                        # Chips that share a line must not collide.
                        chips = sorted(s["chips"], key=lambda x: (x["line"], x["left"]))
                        for a, b in zip(chips, chips[1:]):
                            if a["line"] == b["line"] and b["left"] < a["right"] - 0.5:
                                failures += fail(f"meta chips overlap in {label}: {a['text']!r} / {b['text']!r}")
                        # Each KIND of fact is drawn in its own colour, so the
                        # eye separates them before reading them. Comparing the
                        # class is enough here: the colours are literals in the
                        # class, and this asserts they differ per kind rather
                        # than that a particular palette is in use.
                        colours = {}
                        for ch in s["chips"]:
                            m2 = re.search(r"text-\[(#[0-9a-fA-F]+)\]|text-(accent|muted-foreground|danger)", ch.get("valueCls", ""))
                            if m2:
                                kind = (ch.get("label") or "").lower()
                                colours.setdefault(kind, set()).add(m2.group(0))
                        distinct = {k: sorted(v) for k, v in colours.items()}
                        print(f"      colours by kind: {distinct}")
                        used = [c for v in colours.values() for c in v]
                        if len(used) > 1 and len(set(used)) < len(colours):
                            failures += fail(f"two kinds of fact share a colour in {label}: {distinct}")
                        # Every session fact must NAME itself: `main` beside a
                        # count says nothing about which is a branch and which a
                        # worktree. A chip with no label element is unlabelled.
                        for ch in s["chips"]:
                            if (ch.get("text") or "").strip() and not ch.get("label"):
                                failures += fail(f"session chip in {label} has no label element: {ch['text']!r}")
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
                    # Expand/collapse: click the project header and measure what
                    # the user actually sees. An earlier version of this check
                    # compared the chevron's CLASS STRING and passed while the
                    # icon never moved: Tailwind's `transform` plugin is off, so
                    # `.rotate-90` emitted `translate(var(--tw-translate-x))`
                    # with that variable undefined, the declaration was dropped,
                    # and computed `transform` stayed `none`. So this reads the
                    # computed style and the drawn path, not the class name.
                    if label == "narrow":
                        before_count = await c.js("document.querySelectorAll('.session-row').length")
                        arrow_expanded = await c.js("""(() => {
                            const h = document.querySelector('.project-header');
                            if (!h) return null;
                            const svg = h.querySelector('svg');
                            const pl = svg.querySelector('polyline');
                            const cs = getComputedStyle(svg);
                            return { points: pl ? pl.getAttribute('points') : null,
                                     transform: cs.transform, rect: svg.getBoundingClientRect().width };
                        })()""")
                        await c.js("document.querySelector('.project-header').click()")
                        await asyncio.sleep(0.8)
                        after_count = await c.js("document.querySelectorAll('.session-row').length")
                        arrow_collapsed = await c.js("""(() => {
                            const h = document.querySelector('.project-header');
                            if (!h) return null;
                            const svg = h.querySelector('svg');
                            const pl = svg.querySelector('polyline');
                            const cs = getComputedStyle(svg);
                            return { points: pl ? pl.getAttribute('points') : null,
                                     transform: cs.transform, rect: svg.getBoundingClientRect().width };
                        })()""")
                        print(f"  COLLAPSE: sessions {before_count} -> {after_count}")
                        print(f"            arrow {arrow_expanded['points']!r} -> {arrow_collapsed['points']!r}")
                        if after_count >= before_count:
                            failures += fail(f"collapsing a project did not hide its sessions: {before_count} -> {after_count}")
                        # The arrow must POINT differently in each state. Two
                        # different paths, both actually drawn: a rotation that
                        # never applies would keep the same points.
                        if not arrow_expanded or not arrow_collapsed:
                            failures += fail("the collapse arrow is missing from the project header")
                        elif arrow_expanded["points"] == arrow_collapsed["points"]:
                            failures += fail(f"the arrow does not change direction: {arrow_expanded['points']!r} in both states")
                        # Expanded points DOWN (6 9 12 15 18 9), collapsed points
                        # RIGHT (9 18 15 12 9 6) - the direction the list goes.
                        if arrow_expanded and arrow_expanded["points"] != "6 9 12 15 18 9":
                            failures += fail(f"expanded arrow must point down, got {arrow_expanded['points']!r}")
                        if arrow_collapsed and arrow_collapsed["points"] != "9 18 15 12 9 6":
                            failures += fail(f"collapsed arrow must point right, got {arrow_collapsed['points']!r}")
                        await c.js("document.querySelector('.project-header').click()")
                        await asyncio.sleep(0.8)
                        restored = await c.js("document.querySelectorAll('.session-row').length")
                        print(f"  EXPAND: sessions -> {restored}")
                        if restored != before_count:
                            failures += fail(f"expanding did not restore the sessions: had {before_count}, now {restored}")

                    for p in m["projects"]:
                        if p.get("error"):
                            failures += fail(f"project header unreadable: {p['error']}")
                            continue
                        print(f"  - project {p['titleText']!r}  under={p['under']} title w={p['titleWidth']:.1f}")
                        for ch in p["chips"]:
                            print(f"      chip label={ch.get('label')!r:12} value={ch.get('valueText')!r}")
                        if not p["under"]:
                            failures += fail(f"project facts are not under the title in {label}: {p['titleText']!r}")
                        if not p["titleNotStarved"]:
                            failures += fail(f"project title squeezed in {label}: {p['titleText']!r}")
                        # A project's facts must name themselves too: the whole
                        # complaint was a bare `3` beside an icon nobody could
                        # identify.
                        for ch in p["chips"]:
                            if (ch.get("text") or "").strip() and not ch.get("label"):
                                failures += fail(f"project chip in {label} has no label element: {ch['text']!r}")
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
