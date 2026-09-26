"""Measure the modal backdrop blur of a running motita gateway, in a real browser.

Called by scripts/verify-modal-blur.sh with the gateway URL, the CDP port and an
output directory; it is not meant to be run by hand.

What it proves, and why the screenshot alone is not enough: it takes the SMOOTH
auth modal (no credential), locates the topmost full-screen overlay, reads its
computed `backdrop-filter`, and then measures the backdrop TWICE - once as it is
and once with the declaration forced to `none` - over the same region of the chat
area beside the card. The pair of Laplacian variances (low = smooth = blurred)
is what shows the blur is doing something; a screenshot cannot tell a blurred
backdrop from a dark one.
"""
import asyncio
import base64
import json
import os
import subprocess
import sys

import websockets
from PIL import Image

PORT = int(os.environ.get("CDP_PORT", "9334"))
BASE = os.environ.get("GATEWAY_URL", "http://127.0.0.1:7477")
SHOTS = os.environ.get("SHOTS_DIR", "/tmp/motita-modal-blur")


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
        r = await self.call("Runtime.evaluate", expression=expr, returnByValue=True)
        if r.get("exceptionDetails"):
            raise RuntimeError(str(r["exceptionDetails"]))
        return r.get("result", {}).get("value")

    async def shot(self, path):
        r = await self.call("Page.captureScreenshot", format="png")
        with open(path, "wb") as f:
            f.write(base64.b64decode(r["data"]))
        return path


def lap_var(img, box):
    g = img.convert("L").crop(box)
    px = g.load()
    w, h = g.size
    vals = []
    for y in range(1, h - 1):
        for x in range(1, w - 1):
            vals.append(-4 * px[x, y] + px[x - 1, y] + px[x + 1, y] + px[x, y - 1] + px[x, y + 1])
    m = sum(vals) / len(vals)
    return round(sum((v - m) ** 2 for v in vals) / len(vals), 2)


def busiest_box(img, size=160, step=16, avoid=None, min_x=0):
    g = img.convert("L")
    w, h = g.size
    best, best_box = -1, None
    for y in range(0, h - size, step):
        for x in range(min_x, w - size, step):
            box = (x, y, x + size, y + size)
            if avoid and box[0] < avoid[2] and box[2] > avoid[0] and box[1] < avoid[3] and box[3] > avoid[1]:
                continue
            v = lap_var(g, box)
            if v > best:
                best, best_box = v, box
    return best_box, best


OVERLAY_JS = (
    "(() => { const out = [];"
    " for (const el of document.querySelectorAll('div')) {"
    "  const cs = getComputedStyle(el); if (cs.position !== 'fixed') continue;"
    "  const r = el.getBoundingClientRect();"
    "  if (r.width < innerWidth - 2 || r.height < innerHeight - 2) continue;"
    "  if (cs.backgroundColor === 'rgba(0, 0, 0, 0)') continue;"
    "  out.push(el); }"
    " out.sort((a, b) => (parseInt(getComputedStyle(a).zIndex) || 0)"
    " - (parseInt(getComputedStyle(b).zIndex) || 0));"
    " return out.length ? [out[out.length - 1]] : []; })()"
)


async def main():
    os.makedirs(SHOTS, exist_ok=True)
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
    async with websockets.connect(target["webSocketDebuggerUrl"],
                                  max_size=64 * 1024 * 1024) as ws:
        c = CDP(ws)
        await c.call("Page.enable")
        await c.call("Runtime.enable")
        await c.call("Network.enable")
        await c.call("Network.setCacheDisabled", cacheDisabled=True)
        await c.call("Emulation.setDeviceMetricsOverride",
                     width=390, height=844, deviceScaleFactor=2, mobile=True)
        await c.call("Network.clearBrowserCookies")
        # localStorage is not reachable on the blank tab the probe starts from
        # (the origin is opaque, so reading it throws a SecurityError). It only
        # matters AFTER the navigation, which is where the cookie-less start is
        # enforced anyway.
        await c.call("Page.navigate", url=BASE + "/")
        await asyncio.sleep(3)
        await c.js("(() => { try { localStorage.clear() } catch (e) { } return 'ok'; })()")
        await c.call("Network.clearBrowserCookies")
        await c.call("Page.navigate", url=BASE + "/")
        await asyncio.sleep(4)

        print("health:", subprocess.run(["curl", "-s", f"{BASE}/v1/health"],
                                        capture_output=True, text=True).stdout.strip())
        print("auth modal present:", await c.js(
            "document.body.innerText.includes('Authentication required')"))
        print("css in use:", await c.js(
            "performance.getEntriesByType('resource').map(r=>r.name)"
            ".filter(n=>n.endsWith('.css')).join(',')"))

        card = await c.js(
            "(() => { const d = document.querySelector('div[class*=\"max-w-sm\"]');"
            " if (!d) return null; const r = d.getBoundingClientRect();"
            " return [Math.round(r.x*2), Math.round(r.y*2),"
            " Math.round((r.x+r.width)*2), Math.round((r.y+r.height)*2)]; })()")
        ov = await c.js(
            "(() => { const els = " + OVERLAY_JS + ";"
            " if (!els.length) return null; const cs = getComputedStyle(els[0]);"
            " return {z: cs.zIndex, cls: els[0].getAttribute('class'),"
            " backdropFilter: cs.backdropFilter,"
            " webkit: cs.WebkitBackdropFilter,"
            " varBlur: cs.getPropertyValue('--tw-backdrop-blur')}; })()")
        chat_x = await c.js(
            "(() => { const a = document.querySelector('aside');"
            " if (!a) return 0; return Math.round(a.getBoundingClientRect().right * 2) + 8; })()")
        print("card rect (device px):", card)
        print("chat area starts at x:", chat_x)
        print("overlay:", json.dumps(ov, indent=2))

        shot = f"{SHOTS}/live-final-auth-modal.png"
        await c.shot(shot)
        img = Image.open(shot)
        box, v_blur = busiest_box(img, size=160, avoid=tuple(card), min_x=chat_x)
        await c.js("(() => { for (const el of " + OVERLAY_JS + ") el.style.backdropFilter = 'none';"
                   " return 'ok'; })()")
        control = f"{SHOTS}/live-final-auth-modal-control.png"
        await c.shot(control)
        v_ctl = lap_var(Image.open(control), box)
        await c.js("(() => { for (const el of " + OVERLAY_JS + ") el.style.backdropFilter = '';"
                   " return 'ok'; })()")
        print(f"backdrop window measured      : {box}")
        print(f"lap_var WITH blur             : {v_blur}")
        print(f"lap_var control (blur off)    : {v_ctl}")
        print(f"sharpness ratio control/blur  : "
              f"{round(v_ctl / v_blur, 2) if v_blur > 0 else 'inf (>1000x)'}")
        print("SHOTS:", shot, control)

        await c.call("Emulation.setDeviceMetricsOverride",
                     width=1280, height=800, deviceScaleFactor=1, mobile=False)
        await asyncio.sleep(1.2)
        p = f"{SHOTS}/live-final-auth-modal-1280.png"
        await c.shot(p)
        print("SHOT:", p)
    return 0


sys.exit(asyncio.run(main()))
