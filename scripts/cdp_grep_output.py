"""Probe: a real command whose output is large must reach the terminal drawer whole.

Before this change the agent reported a command's output through truncateMiddle(output, 800):
a `grep` over a tree arrived as ~9 lines and a bare "[... middle omitted ...]", in the chat AND
in the terminal drawer, which is the record of the run. Measured on the reader's own sessions:
49 kept steps carried that mark at exactly 829 bytes.

This drives a scratch gateway with the simulated LLM and asserts, on the wire, what the report
of the command carries.
"""
import asyncio, json, os, sys, urllib.request

sys.path.insert(0, os.path.dirname(__file__))
from cdp_spinner import CDP, start_browser, PORT  # noqa: E402

BASE = os.environ["GATEWAY_URL"]
TOKEN = open(os.environ["GATEWAY_STATE"]).read().strip()
LINES = 700  # ~19 KB, past the 800 bytes the report used to keep
failures = 0


def fail(m):
    global failures; failures += 1; print("  FAIL " + m)


def ok(m):
    print("  ok   " + m)


def api(path, body=None):
    req = urllib.request.Request(BASE + path, method="POST" if body is not None else "GET",
                                 data=json.dumps(body).encode() if body is not None else None,
                                 headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.load(r)


async def until(c, expr, secs=90):
    for _ in range(int(secs / 0.1)):
        if await c.js(expr):
            return True
        await asyncio.sleep(0.1)
    return False


async def send(c, text):
    await c.js("""((t) => {
      const ta = document.querySelector('form textarea');
      const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
      set.call(ta, t); ta.dispatchEvent(new Event('input', { bubbles: true }));
    })(%s)""" % json.dumps(text))
    await asyncio.sleep(0.2)
    await c.js("document.querySelector('form').requestSubmit()")


async def main(ws_url):
    import websockets
    async with websockets.connect(ws_url, max_size=64 << 20) as ws:
        c = CDP(ws)
        await c.call("Page.enable")
        await c.call("Network.setCacheDisabled", cacheDisabled=True)
        await c.call("Emulation.setDeviceMetricsOverride", width=1280, height=800, deviceScaleFactor=1, mobile=False)
        await c.call("Page.navigate", url="about:blank")
        await c.call("Page.navigate", url=f"{BASE}/#t={TOKEN}")
        await asyncio.sleep(2.5)
        await c.js("""(async () => {
          for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister();
          for (const k of await caches.keys()) await caches.delete(k);
        })()""")
        await c.call("Page.navigate", url="about:blank")
        await c.call("Page.navigate", url=f"{BASE}/#t={TOKEN}")
        await until(c, "!!document.querySelector('form textarea')", 20)

        print(f"== a command that prints ~{LINES} lines (~19 KB) ==")
        ready = await until(c, "!!document.querySelector('form textarea')", 20)
        if not ready:
            print("    page state:", await c.js("({title: document.title, forms: document.querySelectorAll('form').length, body: document.body.innerText.slice(0,300)})"))
            fail("the composer never appeared")
            return 1
        await send(c, "grep the tree")
        await until(c, "!document.querySelector('.msg.activity') && document.querySelectorAll('.msg.agent').length >= 1", 150)
        await asyncio.sleep(1.0)

        cps = api("/v1/sessions/default/checkpoints")["checkpoints"]
        cmds = [st for cp in cps for st in cp["steps"] if st["kind"] == "command"]
        if not cmds:
            fail("the run left no command step")
            return 1
        st = cmds[0]
        out = st.get("out") or ""
        print(f"    stored output: {len(out)} bytes, {out.count(chr(10)) + 1} lines")
        if "omitted" in out or "output cut" in out:
            fail(f"the output was cut: {out[:80]!r} ... {out[-60:]!r}")
        else:
            ok("the output was kept whole in the run's record")
        if "line-0001" not in out or f"line-{LINES:04d}" not in out:
            fail("the ends of the output are not both there")
        else:
            ok("both ends of the output are there")

        # What the page has: the terminal drawer replays the run from those steps.
        await c.js("document.querySelector('.term-drawer:not(.is-think) .term-tab').click()")
        # The drawer TYPES what it replays, at up to ~900 characters per second: reading it too
        # early measures the typewriter, not the record. Wait until it stops growing.
        tty, stable = "", 0
        for _ in range(300):
            await asyncio.sleep(0.25)
            now = await c.js("document.querySelector('.term-drawer:not(.is-think) .term-screen')?.textContent ?? ''")
            stable = stable + 1 if len(now) == len(tty) else 0
            tty = now
            if len(tty) > 0 and stable >= 3 and f"line-{LINES:04d}" in tty:
                break
        print(f"    terminal drawer: {len(tty)} chars")
        if "line-0001" in tty and f"line-{LINES:04d}" in tty:
            ok("the terminal shows the command's output from start to end")
        else:
            fail("the terminal is missing the ends of the output")
        if "omitted" in tty or "output cut" in tty:
            fail("the terminal still shows a cut")
        else:
            ok("no cut marker in the terminal")
        await c.shot(f"{os.environ.get('SHOTS_DIR', '/tmp')}/grep-terminal.png")

    print("VERDICT: " + ("ok" if failures == 0 else f"FAILED ({failures})"))
    return 1 if failures else 0


if __name__ == "__main__":
    os.makedirs(os.environ.get("SHOTS_DIR", "/tmp"), exist_ok=True)
    start_browser()
    with urllib.request.urlopen(f"http://127.0.0.1:{PORT}/json/list", timeout=5) as r:
        page = [t for t in json.load(r) if t.get("type") == "page"][0]
    sys.exit(asyncio.run(main(page["webSocketDebuggerUrl"])))
