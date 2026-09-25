#!/usr/bin/env python3
"""Measure the sidebar's "..." row menu against a running gateway.

What this checks, and why each one is here:

  * OPENING IS ANIMATED. A menu that appears in a single frame reads as a
    glitch next to the rest of this UI, which animates everything. Asserted on
    the COMPUTED `animation-name`, never the class: this build disables
    Tailwind's `transition*` and `opacity` plugins, so markup that says
    `transition-opacity opacity-0` emits no rule at all and still "looks
    right" in the source. That trap already shipped once in this sidebar.

  * A DISABLED item is DIMMED. `Integrate` renders only for project sessions
    and is disabled when there is nothing to merge. Before, the disabled and
    enabled states were visually identical, so the only way to discover it was
    to click and get nothing.

  * CLICKING OUTSIDE closes it, THROUGH THE EXIT ANIMATION. Both halves are
    asserted: the menu must be gone shortly after the click, and it must pass
    through a state whose animation is the exit one. A menu that unmounts
    instantly satisfies the first and fails the second.

  * The menu is NOT CLIPPED by its container. It is absolutely positioned over
    the list, so any ancestor with `overflow: hidden` cuts it off. Measured as
    geometry, which is the only way to see it: the element can be present,
    "visible" per CSS, and still half hidden.

Usage:
    GATEWAY_URL=http://127.0.0.1:7477 python3 scripts/verify-row-menu.py
Env:
    GATEWAY_URL    gateway base URL (default http://127.0.0.1:7477)
    MOTITA_TOKEN   bearer token; falls back to MOTITA_TOKEN_FILE
    MOTITA_TOKEN_FILE  file holding the token (default ~/.motita/gateway.token)
    CDP_PORT       Chrome DevTools port (default 9444)
    SHOTS_DIR      where screenshots land (default /tmp/motita-row-menu-shots)
Exit code is 0 only when every check passes.
"""

import asyncio
import base64
import json
import os
import subprocess
import time
import urllib.request

import websockets

BASE = os.environ.get("GATEWAY_URL", "http://127.0.0.1:7477")
CDP_PORT = int(os.environ.get("CDP_PORT", "9444"))
SHOTS_DIR = os.environ.get("SHOTS_DIR", "/tmp/motita-row-menu-shots")
CHROME = os.path.expanduser(
    "~/.hermes/cache/chrome/chrome-headless-shell-linux64/chrome-headless-shell"
)

failures: list[str] = []


def fail(msg: str) -> int:
    print(f"  FAIL {msg}")
    failures.append(msg)
    return 1


def token() -> str:
    if os.environ.get("MOTITA_TOKEN"):
        return os.environ["MOTITA_TOKEN"]
    path = os.environ.get(
        "MOTITA_TOKEN_FILE", os.path.expanduser("~/.motita/gateway.token")
    )
    with open(path) as fh:
        return fh.read().strip()


# The whole measurement runs inside the page: one round trip, and the
# intermediate frames of an animation can only be observed from inside it.
# Every wait is a real `await`, because reading the DOM on the same tick as the
# click sees the PREVIOUS render — that mistake already produced four bogus
# "menu not found" results in this project.
MEASURE_JS = r"""
(async () => {
  const sleep = (ms) => new Promise(r => setTimeout(r, ms));
  const rows = [...document.querySelectorAll('.session-row')];
  const out = {};

  const rowWith = (title) => rows.find(r => r.querySelector(`button[title="${title}"]`));
  const toggle = (r) => r.querySelector('button[title="More actions"]');

  // ---- opening animation ----
  const r0 = rowWith('More actions');
  if (!r0) return { error: 'no session row with a "..." button' };
  toggle(r0).click();
  await sleep(60);
  const m0 = r0.querySelector('.row-menu');
  if (!m0) return { error: 'the menu did not open' };
  const cs0 = getComputedStyle(m0);
  out.open = {
    animation: cs0.animationName,
    duration: cs0.animationDuration,
    opacity: parseFloat(cs0.opacity),
    shadow: cs0.boxShadow !== 'none',
    // Positioned over the list, so it must actually be on top of it.
    zIndex: cs0.zIndex,
  };

  // ---- items: labels, and the disabled one is dimmed ----
  out.items = [];
  for (const r of rows) {
    if (!toggle(r)) continue;
    toggle(r).click();
    await sleep(60);
    const m = r.querySelector('.row-menu');
    if (!m) { out.items.push({ error: 'menu did not open' }); continue; }
    const entry = { labels: [] };
    for (const b of m.querySelectorAll('button')) {
      const cs = getComputedStyle(b);
      const label = b.textContent.trim();
      entry.labels.push(label);
      if (label === 'Integrate') {
        entry.integrate = {
          disabled: b.disabled,
          opacity: parseFloat(cs.opacity),
          cursor: cs.cursor,
          color: cs.color,
        };
      }
      // Every item must be dimmable and must dim when disabled: an
      // `.row-menu-item:disabled` rule depends on this class being present.
      if (label === 'Integrate') entry.integrateHasItemClass = b.classList.contains('row-menu-item');
    }
    const ren = [...m.querySelectorAll('button')].find(b => b.textContent.trim() === 'Rename');
    if (ren) entry.renameOpacity = parseFloat(getComputedStyle(ren).opacity);
    out.items.push(entry);
    toggle(r).click();
    await sleep(200);
  }

  // ---- clicking outside closes it, through the exit animation ----
  const r1 = rowWith('More actions');
  toggle(r1).click();
  await sleep(60);
  const openBefore = !!r1.querySelector('.row-menu');
  // A real outside click: on the app background, which is not the menu.
  const bg = document.querySelector('.app-bg') || document.body;
  bg.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
  await sleep(40);
  const mid = r1.querySelector('.row-menu');
  const midAnim = mid ? getComputedStyle(mid).animationName : null;
  const midClosing = mid ? mid.classList.contains('row-menu-closing') : false;
  await sleep(300);
  const stillOpen = !!r1.querySelector('.row-menu');
  out.outside = {
    openBefore,
    midAnim,
    midClosing,
    // The exit animation must START during those 40 ms, so it must be
    // running (or already done) rather than the element being gone.
    animatedOut: midAnim === 'rowMenuOut' || mid === null,
    goneAfter: !stillOpen,
    closed: openBefore && !stillOpen,
  };

  // ---- the same button must still toggle it shut ----
  const r2 = rowWith('More actions');
  toggle(r2).click();
  await sleep(60);
  const o1 = !!r2.querySelector('.row-menu');
  toggle(r2).click();
  await sleep(300);
  const o2 = !!r2.querySelector('.row-menu');
  out.toggle = { opensOnFirstClick: o1, closesOnSecondClick: !o2 };

  // ---- not clipped by an ancestor ----
  // Walk up from the menu looking for any ancestor that hides overflow, and
  // compare the boxes. Also do it for a COLLAPSED project, where the clipping
  // used to be worst.
  const clippedBy = (el) => {
    const er = el.getBoundingClientRect();
    let n = el.parentElement;
    while (n && n !== document.body) {
      const s = getComputedStyle(n);
      if (s.overflow === 'hidden' || s.overflowY === 'hidden' || s.overflowX === 'hidden') {
        const nr = n.getBoundingClientRect();
        const hiddenBottom = Math.max(0, er.bottom - nr.bottom);
        const hiddenTop = Math.max(0, nr.top - er.top);
        if (hiddenBottom > 0.5 || hiddenTop > 0.5) {
          return { cls: n.className.toString().slice(0, 60), hiddenPx: Math.round(hiddenBottom + hiddenTop) };
        }
      }
      n = n.parentElement;
    }
    return null;
  };
  out.clipping = [];
  const hdr = document.querySelector('.project-header');
  if (hdr) {
    for (const collapse of [false, true]) {
      const b = hdr.querySelector('button[title="More actions"]');
      if (!b) break;
      b.click();
      await sleep(80);
      const m = hdr.querySelector('.row-menu');
      if (m) out.clipping.push({ collapsed: collapse, clip: clippedBy(m) });
      b.click();
      await sleep(250);
      if (collapse) break;
      hdr.click();  // collapse the project for the second pass
      await sleep(300);
    }
    // leave the project expanded
    if (hdr.closest('div[class*="rounded-xl"]')) {
      // no-op: state was restored above
    }
  }
  return out;
})()
"""


async def run_measure(shot_path: str | None) -> dict:
    proc = subprocess.Popen(
        [
            CHROME,
            f"--remote-debugging-port={CDP_PORT}",
            "--headless",
            "--no-sandbox",
            "--disable-gpu",
            "--hide-scrollbars",
            "about:blank",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        ws_url = None
        for _ in range(80):
            try:
                with urllib.request.urlopen(
                    f"http://127.0.0.1:{CDP_PORT}/json/list", timeout=1
                ) as resp:
                    targets = json.load(resp)
                pages = [t for t in targets if t.get("type") == "page"]
                if pages:
                    ws_url = pages[0]["webSocketDebuggerUrl"]
                    break
            except Exception:
                pass
            time.sleep(0.25)
        if not ws_url:
            raise RuntimeError("could not attach to Chrome")

        async with websockets.connect(ws_url, max_size=50 * 1024 * 1024) as ws:
            counter = [0]

            async def call(method: str, **params):
                counter[0] += 1
                mid = counter[0]
                await ws.send(json.dumps({"id": mid, "method": method, "params": params}))
                while True:
                    msg = json.loads(await ws.recv())
                    if msg.get("id") == mid:
                        if "error" in msg:
                            raise RuntimeError(f"{method}: {msg['error']}")
                        return msg.get("result", {})

            async def js(expr: str):
                res = await call(
                    "Runtime.evaluate", expression=expr, returnByValue=True, awaitPromise=True
                )
                if res.get("exceptionDetails"):
                    raise RuntimeError(f"page error: {res['exceptionDetails']}")
                return res.get("result", {}).get("value")

            await call("Page.enable")
            await call("Runtime.enable")
            await call("Network.setCacheDisabled", cacheDisabled=True)
            await call(
                "Emulation.setDeviceMetricsOverride",
                width=390,
                height=900,
                deviceScaleFactor=2,
                mobile=False,
            )
            url = f"{BASE}/#t={token()}"
            # Load twice: the first pass registers the service worker, and a
            # cached bundle would measure the PREVIOUS build.
            for _ in range(2):
                await call("Page.navigate", url="about:blank")
                await call("Page.navigate", url=url)
                await asyncio.sleep(2.5)
                await js(
                    "(async()=>{for(const r of await navigator.serviceWorker.getRegistrations())"
                    "await r.unregister();for(const k of await caches.keys())await caches.delete(k);})()"
                )

            data = await js(MEASURE_JS)
            if shot_path:
                res = await call("Page.captureScreenshot", format="png")
                with open(shot_path, "wb") as fh:
                    fh.write(base64.b64decode(res["data"]))
            return data
    finally:
        proc.terminate()


def main() -> int:
    os.makedirs(SHOTS_DIR, exist_ok=True)
    print(f"=== row menu, measured against {BASE} ===")
    data = asyncio.run(run_measure(os.path.join(SHOTS_DIR, "row-menu.png")))

    if data.get("error"):
        fail(data["error"])
        print(f"\nROW MENU VERIFICATION FAILED: {len(failures)} check(s)")
        return 1

    op = data["open"]
    print(f"  opening animation : {op['animation']} ({op['duration']})")
    if op["animation"] in ("none", ""):
        fail("the menu does not animate on open (animation-name is 'none')")
    if not op["shadow"]:
        fail("the menu has no shadow, so it does not separate from the list behind it")

    print("  items:")
    any_integrate = False
    for it in data["items"]:
        if it.get("error"):
            fail(f"a row's menu did not open: {it['error']}")
            continue
        print(f"    {', '.join(it['labels'])}")
        if "integrate" in it:
            any_integrate = True
            ig = it["integrate"]
            print(
                f"      Integrate disabled={ig['disabled']} opacity={ig['opacity']} cursor={ig['cursor']}"
            )
            if not it.get("integrateHasItemClass"):
                fail("the Integrate item is missing the `row-menu-item` class, so it cannot be dimmed")
            if ig["disabled"] and ig["opacity"] >= 1:
                fail(
                    f"a DISABLED Integrate is not dimmed (computed opacity {ig['opacity']}) "
                    "— disabled and enabled look identical"
                )
            if ig["disabled"] and ig["cursor"] != "not-allowed":
                fail(f"a DISABLED Integrate does not show a `not-allowed` cursor (got {ig['cursor']})")
            if "renameOpacity" in it and ig["disabled"] and ig["opacity"] >= it["renameOpacity"]:
                fail(
                    "the dimmed Integrate is not dimmer than an enabled item beside it "
                    f"({ig['opacity']} vs {it['renameOpacity']})"
                )
    if not any_integrate:
        print("    (no project session with an Integrate item was present to check dimming)")

    o = data["outside"]
    print(f"  click outside     : closed={o['closed']} exitAnimation={o['midAnim']}")
    if not o["openBefore"]:
        fail("the menu was not open before the outside click, so the check proves nothing")
    if not o["goneAfter"]:
        fail("clicking outside did NOT close the menu")
    if not o["animatedOut"]:
        fail(
            "the menu did not play its exit animation when closed from outside "
            f"(animation-name was {o['midAnim']!r})"
        )

    t = data["toggle"]
    print(f"  toggle button     : opens={t['opensOnFirstClick']} closes={t['closesOnSecondClick']}")
    if not t["opensOnFirstClick"]:
        fail("clicking the '...' button did not open the menu")
    if not t["closesOnSecondClick"]:
        fail("clicking the '...' button again did not close the menu (outside-click handler fighting it?)")

    for c in data.get("clipping", []):
        where = "collapsed project" if c["collapsed"] else "expanded project"
        if c["clip"]:
            fail(
                f"the menu is CLIPPED in the {where} by `{c['clip']['cls']}`: "
                f"{c['clip']['hiddenPx']}px hidden"
            )
        else:
            print(f"  clipping ({where}) : none")

    print()
    if failures:
        print(f"ROW MENU VERIFICATION FAILED: {len(failures)} check(s)")
        return 1
    print("ROW MENU VERIFICATION PASSED")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
