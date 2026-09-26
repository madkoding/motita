"""Measure the scheduled-tasks panel of a running motita gateway, in a real browser.

Driven by scripts/verify-tasks-layout.sh; not meant to be run by hand.

What it proves, and why a screenshot is not enough:

  * The layout is ONE panel with TWO arrangements. Phone (390px): a single
    column, the list above the create form, the list scrolling at 45vh. Desktop
    (1440px): a grid, the form in the LEFT column and the list in the RIGHT
    one, the two not overlapping and both inside the card.
  * The cadence is a TAG and the row's headline figure is a COUNTDOWN to the
    next run - so the tag must carry the cadence and the countdown must be a
    chronometer that MOVES between two samples seconds apart. A static
    "next run <date>" would pass a screenshot and fail this.
  * A paused task shows no countdown: its next run is a projection, and a
    ticker over a task that will not fire is a lie with a pulse.
"""
import asyncio
import base64
import json
import os
import re
import subprocess
import sys

import websockets

PORT = int(os.environ.get("CDP_PORT", "9344"))
BASE = os.environ.get("GATEWAY_URL", "http://127.0.0.1:7479")
SHOTS = os.environ.get("SHOTS_DIR", "/tmp/motita-tasks-layout")

fail = 0


def ok(msg):
    print("  ok    " + msg)


def bad(msg):
    global fail
    fail += 1
    print("  FAIL  " + msg)


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


# The panel is opened by the sidebar button, and the sidebar button is what the
# user presses: driving the state directly would prove a layout that the real
# path might never reach.
OPEN_JS = """
(async () => {
  const sleep = (ms) => new Promise(r => setTimeout(r, ms));
  const byText = (re) => {
    for (const b of document.querySelectorAll('button')) {
      if (re.test((b.innerText || '').trim())) return b;
    }
    return null;
  };
  const byLabel = (re) => {
    for (const b of document.querySelectorAll('button')) {
      const l = (b.getAttribute('aria-label') || '') + ' ' + (b.title || '');
      if (re.test(l)) return b;
    }
    return null;
  };
  let open = byText(/^Scheduled tasks$/i);
  if (!open) {
    // The sidebar is off canvas on a phone: reveal it and look again.
    const menu = byLabel(/open sidebar|menu|sidebar/i);
    if (menu) { menu.click(); await sleep(500); open = byText(/^Scheduled tasks$/i); }
  }
  if (!open) return 'no Scheduled tasks button (' +
    [...document.querySelectorAll('button')].map(b => (b.innerText || b.getAttribute('aria-label') || '?').trim()).join(' / ').slice(0, 400) + ')';
  open.click();
  await sleep(700);
  return document.querySelector('.tasks-body') ? 'open' : 'the panel did not open';
})()
"""

# One read of everything the assertions need, measured off the live DOM: the
# computed styles and the geometry, never the class strings.
MEASURE_JS = """
(() => {
  const q = (s) => document.querySelector(s);
  const rect = (el) => { const r = el.getBoundingClientRect();
    return {x: r.x, y: r.y, w: r.width, h: r.height, right: r.right, bottom: r.bottom}; };
  const panel = q('.tasks-panel'), body = q('.tasks-body'), form = q('.tasks-form');
  const col = q('.tasks-list-column'), list = q('.tasks-list');
  if (!panel || !body || !form || !col) return null;
  const rows = [...document.querySelectorAll('.tasks-list-column > .tasks-list > div')];
  const rowInfo = rows.map(el => {
    const tag = el.querySelector('.task-tag');
    const cd = el.querySelector('.task-countdown');
    return {
      title: (el.querySelector('span') || {}).textContent || '',
      tag: tag ? tag.textContent.trim() : null,
      tagDisplay: tag ? getComputedStyle(tag).display : null,
      tagFontSize: tag ? getComputedStyle(tag).fontSize : null,
      countdown: cd ? cd.textContent.trim() : null,
      hasCountdownText: /fires in|due now/.test(el.innerText),
      text: el.innerText.replace(/\\n/g, ' | '),
    };
  });
  // A horizontal scrollbar INSIDE a column is a defect, not a detail: it means a child is
  // wider than the column, and it paints a bar across the whole card. Reported as the
  // overflow in pixels so the assertion can require it to be zero.
  const spill = [];
  const scan = (el, name) => {
    if (!el) return;
    const over = el.scrollWidth - el.clientWidth;
    if (over > 0) spill.push({what: name, over: over});
    const cs = getComputedStyle(el);
    const edge = el.getBoundingClientRect().x + el.clientLeft + el.clientWidth;
    for (const kid of el.querySelectorAll('*')) {
      const b = kid.getBoundingClientRect();
      if (b.right > edge + 0.5) spill.push({what: name + ' > ' + kid.tagName + '.' + String(kid.className).slice(0, 30),
        over: +(b.right - edge).toFixed(2)});
    }
    void cs;
  };
  scan(form, '.tasks-form');
  scan(col, '.tasks-list-column');
  scan(panel, '.tasks-panel');
  return {
    viewport: {w: innerWidth, h: innerHeight},
    bodyDisplay: getComputedStyle(body).display,
    bodyCols: getComputedStyle(body).gridTemplateColumns,
    panel: rect(panel), form: rect(form), col: rect(col), list: rect(list),
    listOverflowY: getComputedStyle(list).overflowY,
    listMaxHeight: getComputedStyle(list).maxHeight,
    countdownColor: (q('.task-countdown') ? getComputedStyle(q('.task-countdown')).color : null),
    countdownFont: (q('.task-countdown') ? getComputedStyle(q('.task-countdown')).fontFamily : null),
    spill: spill,
    rows: rowInfo,
  };
})()
"""


async def main():
    os.makedirs(SHOTS, exist_ok=True)
    token = ""
    gw = os.environ.get("GATEWAY_STATE", "")
    if gw and os.path.exists(gw):
        token = json.load(open(gw))["token"]
    if not token:
        print("NO_TOKEN")
        return 1

    subprocess.run(["curl", "-s", "-X", "PUT", f"http://127.0.0.1:{PORT}/json/new?about:blank"],
                   capture_output=True)
    target = None
    for _ in range(60):
        out = subprocess.run(["curl", "-s", f"http://127.0.0.1:{PORT}/json/list"],
                             capture_output=True, text=True).stdout
        pages = [t for t in json.loads(out or "[]") if t.get("type") == "page"]
        target = pages[-1] if pages else None
        if target:
            break
        await asyncio.sleep(0.25)
    if not target:
        print("NO_TARGET")
        return 1

    async with websockets.connect(target["webSocketDebuggerUrl"], max_size=64 * 1024 * 1024) as ws:
        c = CDP(ws)
        await c.call("Page.enable")
        await c.call("Runtime.enable")
        await c.call("Network.enable")
        await c.call("Network.setCacheDisabled", cacheDisabled=True)
        await c.call("Network.clearBrowserCookies")

        results = {}
        for label, w, h, mobile in (("mobile", 390, 844, True), ("desktop", 1440, 900, False)):
            await c.call("Emulation.setDeviceMetricsOverride",
                         width=w, height=h, deviceScaleFactor=1, mobile=mobile)
            # A fragment-only navigation does not re-run the app's startup: go
            # through about:blank so the token is exchanged for a fresh cookie.
            await c.call("Page.navigate", url="about:blank")
            await asyncio.sleep(0.4)
            await c.call("Page.navigate", url=f"{BASE}/#t={token}")
            await asyncio.sleep(3.5)
            # The service worker serves the OLD bundle after a rebuild.
            await c.js("(async () => { for (const r of await navigator.serviceWorker.getRegistrations())"
                       " await r.unregister(); for (const k of await caches.keys()) await caches.delete(k);"
                       " return 'ok'; })()")
            if not await c.js("document.body.innerText.includes('Scheduled tasks')"):
                await c.call("Page.reload", ignoreCache=True)
                await asyncio.sleep(3.5)
            state = await c.js(OPEN_JS)
            if state != "open":
                bad(f"{label}: the panel did not open ({state})")
                continue
            await asyncio.sleep(0.6)
            m = await c.js(MEASURE_JS)
            if not m:
                bad(f"{label}: nothing to measure (panel closed again?)")
                continue
            results[label] = m
            await c.shot(f"{SHOTS}/tasks-{label}.png")

        # The chronometer ticks: two reads 2.5s apart must differ, or it is a date.
        m1 = await c.js(MEASURE_JS)
        await asyncio.sleep(2.5)
        m2 = await c.js(MEASURE_JS)
        results["tick"] = {"before": m1, "after": m2}
        await c.shot(f"{SHOTS}/tasks-desktop-2.png")

    print(json.dumps(results, indent=2)[:12000])

    d = results.get("desktop")
    mo = results.get("mobile")
    if mo:
        # A phone shows ONE column: normal flow (display: block), list first.
        if mo["bodyDisplay"] != "grid" and mo["form"]["y"] > mo["col"]["y"]:
            ok("mobile: one column, the list above the create form")
        else:
            bad(f"mobile: display={mo['bodyDisplay']} form.y={mo['form']['y']:.0f} "
                f"list.y={mo['col']['y']:.0f} (expected a stacked column with the list first)")
        if mo["listOverflowY"] == "auto":
            ok(f"mobile: the list scrolls on its own (max-height {mo['listMaxHeight']})")
        else:
            bad(f"mobile: the list does not scroll (overflow-y {mo['listOverflowY']})")
        if mo["form"]["right"] <= mo["viewport"]["w"] + 1 and mo["col"]["right"] <= mo["viewport"]["w"] + 1:
            ok("mobile: both blocks fit the viewport")
        else:
            bad("mobile: a block overflows the viewport")
        if abs(mo["form"]["w"] - mo["col"]["w"]) < 2:
            ok(f"mobile: both blocks span the full width ({mo['form']['w']:.0f}px)")
        else:
            bad(f"mobile: the blocks have different widths ({mo['form']['w']:.0f} vs {mo['col']['w']:.0f})")
    if d:
        if d["bodyDisplay"] == "grid":
            ok(f"desktop: the body is a grid ({d['bodyCols']})")
        else:
            bad(f"desktop: the body is not a grid (display {d['bodyDisplay']})")
        if d["form"]["x"] < d["col"]["x"]:
            ok(f"desktop: the form is LEFT of the list (form.x {d['form']['x']:.0f} < list.x {d['col']['x']:.0f})")
        else:
            bad(f"desktop: the form is not left of the list (form.x {d['form']['x']:.0f}, list.x {d['col']['x']:.0f})")
        if abs(d["form"]["y"] - d["col"]["y"]) < 2:
            ok("desktop: both columns start on the same line")
        else:
            bad(f"desktop: the columns are not aligned (form.y {d['form']['y']:.0f}, list.y {d['col']['y']:.0f})")
        if d["form"]["right"] <= d["col"]["x"]:
            ok("desktop: the columns do not overlap")
        else:
            bad("desktop: the columns overlap")
        if d["panel"]["w"] <= 56 * 16 + 1:
            ok(f"desktop: the panel stays inside its 56rem ceiling ({d['panel']['w']:.0f}px)")
        else:
            bad(f"desktop: the panel is {d['panel']['w']:.0f}px, past the 56rem ceiling")
        if d["panel"]["h"] <= d["viewport"]["h"] - 30:
            ok(f"desktop: the panel fits the viewport ({d['panel']['h']:.0f}px of {d['viewport']['h']})")
        else:
            bad(f"desktop: the panel is taller than the viewport ({d['panel']['h']:.0f} > {d['viewport']['h']})")
    for label, m in (("mobile", mo), ("desktop", d)):
        if not m:
            continue
        # No horizontal scrollbar inside a column: a child wider than its column paints a bar
        # across the whole card and clips the content beside it.
        if not m.get("spill"):
            ok(f"{label}: no column spills horizontally")
        else:
            bad(f"{label}: a column overflows horizontally: {m['spill'][:4]}")
        tags = [r["tag"] for r in m["rows"] if r["tag"]]
        if len(tags) == len(m["rows"]):
            ok(f"{label}: every row carries the cadence as a tag ({tags})")
        else:
            bad(f"{label}: a row has no cadence tag ({m['rows']})")
        # The gateway sends the normalised Go form ("24h0m0s" for "24h"): a tag
        # that prints it is showing the machine's spelling of the user's choice.
        raw = [t for t in tags if re.search(r"\d+(h|m|s)\d+[hms]0", t)]
        if tags and not raw:
            ok(f"{label}: the tag is written as the user typed it ({tags})")
        else:
            bad(f"{label}: the tag shows the raw Go duration form ({raw})")
        cds = [r["countdown"] for r in m["rows"] if r["countdown"]]
        if cds and all(":" in x for x in cds):
            ok(f"{label}: the countdown is a chronometer {cds}")
        else:
            bad(f"{label}: no chronometer countdown on the enabled rows ({m['rows']})")
        tag_css = m["rows"][0]["tagFontSize"] if m["rows"] else None
        if tag_css and float(tag_css.rstrip("px")) <= 11:
            ok(f"{label}: the tag is recessed ({tag_css}), not the row's headline")
        else:
            bad(f"{label}: the tag is not styled as a tag ({tag_css})")
    tk = results.get("tick")
    if tk:
        b = [r["countdown"] for r in tk["before"]["rows"] if r["countdown"]]
        a = [r["countdown"] for r in tk["after"]["rows"] if r["countdown"]]
        if b and a and all(x != y for x, y in zip(b, a)):
            ok(f"desktop: the countdown MOVES between samples ({b} -> {a})")
        else:
            bad(f"desktop: the countdown is static ({b} -> {a})")
        paused = [r for r in tk["after"]["rows"] if "Paused" in r["text"]]
        if not paused:
            # Not a failure: whether a paused task exists is a property of the TASKS IN THE
            # GATEWAY, not of the build. A fixture with a paused one exercises the rule; a live
            # gateway whose tasks are all enabled simply has nothing to check, and reporting a
            # failure there would blame the code for the data.
            print("  skip  desktop: no paused task in this gateway, so the rule was not exercised")
        elif all(not r["countdown"] for r in paused):
            ok("desktop: a paused task shows no countdown")
        else:
            bad(f"desktop: a paused task is counting down ({paused})")

    print("SHOTS:", ", ".join(sorted(os.path.join(SHOTS, f) for f in os.listdir(SHOTS))))
    if fail:
        print(f"VERDICT: FAILED ({fail})")
    else:
        print("VERDICT: the panel is two columns on a desktop and one on a phone, "
              "with a tag and a moving countdown per row")
    return 1 if fail else 0


sys.exit(asyncio.run(main()))
