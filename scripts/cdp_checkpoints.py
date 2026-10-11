"""Probe the thinking drawer, the kept steps and the checkpoints on a scratch gateway.

Driven by scripts/verify-checkpoints.sh, which starts the gateway (isolated HOME, simulated LLM,
git workspace) and the browser. Everything asserted here is MEASURED on the rendered page or on
the workspace's files:

  * THINKING: the reasoning goes to the orange THINK drawer, never to the chat.
  * STEPS: the steps of a turn stay under its input after the answer, and after a reload.
  * STICKY: scrolled into a turn, its input sits pinned at the top of the chat; scrolled
    above it, the previous input takes the place.
  * CHECKPOINT: the ⋯ menu goes back (conversation only, then conversation and files), and
    the file in the workspace is what it was before that input.
"""
import asyncio, json, os, pathlib, sys, tempfile

sys.path.insert(0, os.path.dirname(__file__))
from cdp_http import fetch_json  # noqa: E402
from cdp_spinner import CDP, start_browser, PORT  # noqa: E402

BASE = os.environ["GATEWAY_URL"]
TOKEN = open(os.environ["GATEWAY_STATE"]).read().strip()
WORK = os.environ["WORKSPACE"]
SHOTS = os.environ.get("SHOTS_DIR") or tempfile.mkdtemp(prefix="motita-checkpoints-")
failures = 0


def fail(msg):
    global failures
    failures += 1
    print("  FAIL " + msg)


def ok(msg):
    print("  ok   " + msg)


def api(path, body=None):
    return fetch_json(BASE + path, body, timeout=10,
                      headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})


def report():
    p = os.path.join(WORK, "report.txt")
    return open(p).read() if os.path.exists(p) else None


async def until(c, expr, secs=60):
    for _ in range(int(secs / 0.1)):
        if await c.js(expr):
            return True
        await asyncio.sleep(0.1)
    return False


async def scroll_to(c, y):
    await c.call("Input.dispatchMouseEvent", type="mouseWheel", x=700, y=300, deltaX=0, deltaY=-400)
    await asyncio.sleep(0.1)
    await c.js(f"document.querySelector('main').scrollTop = {y}")


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
        await c.call("Emulation.setDeviceMetricsOverride", width=1280, height=640, deviceScaleFactor=1, mobile=False)
        # about:blank first: a navigation that differs only in the fragment does not reload.
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

        print("== 1. a run: the thinking goes to the drawer, the steps to the chat ==")
        await send(c, "write the report")
        saw_live = await until(c, "document.querySelectorAll('.is-think .think-entry.is-live').length > 0", 40)
        await c.js("document.querySelector('.is-think .term-tab').click()")
        await asyncio.sleep(0.5)
        await c.shot(f"{SHOTS}/1-think-open-live.png")
        (ok if saw_live else fail)("the reasoning is written live in the THINK drawer")
        await until(c, "!document.querySelector('.msg.activity') && document.querySelectorAll('.msg.agent').length >= 1", 120)
        await asyncio.sleep(1.0)
        n_think = await c.js("document.querySelectorAll('.is-think .think-entry').length")
        in_chat = await c.js("document.querySelectorAll('main .thought-live, main .trail-step.thought').length")
        (ok if n_think >= 1 else fail)(f"finished thoughts kept in the drawer: {n_think}")
        (ok if in_chat == 0 else fail)(f"thoughts in the chat: {in_chat}")
        await c.shot(f"{SHOTS}/2-think-open-after.png")
        await c.js("document.querySelector('.is-think .term-tab').click()")
        steps1 = await c.js("document.querySelectorAll('main .turn .trail-step').length")
        (ok if steps1 >= 1 else fail)(f"steps still under the input after the answer: {steps1}")
        print(f"    report.txt after input 1: {report()!r}")

        # The terminal: its tab still opens it, and opening one closes the other.
        await c.js("document.querySelector('.term-drawer:not(.is-think) .term-tab').click()")
        await asyncio.sleep(0.5)
        both = await c.js("[...document.querySelectorAll('.term-drawer.is-open')].length")
        (ok if both == 1 else fail)(f"drawers open at once: {both}")
        await c.shot(f"{SHOTS}/3-tty-open.png")
        await c.js("document.querySelector('.term-drawer:not(.is-think) .term-tab').click()")

        print("== 2. a second input, then a reload: nothing is lost ==")
        # Change the file by hand, so going back with files must undo it.
        await asyncio.to_thread(pathlib.Path(WORK, "report.txt").write_text, "edited between the two inputs")
        before_two = report()
        await send(c, "write the report again")
        await until(c, "document.querySelectorAll('.msg.agent').length >= 2 && !document.querySelector('.msg.activity')", 120)
        await asyncio.sleep(1.0)
        after_two = report()
        print(f"    report.txt when input 2 was sent: {before_two!r}; after it ran: {after_two!r}")
        (ok if after_two != before_two else fail)("input 2 changed the file (so going back has something to undo)")
        await c.call("Page.reload", ignoreCache=True)
        await until(c, "document.querySelectorAll('main .turn-head').length >= 2", 20)
        await asyncio.sleep(1.5)
        heads = await c.js("document.querySelectorAll('main .turn-head').length")
        steps = await c.js("[...document.querySelectorAll('main .turn')].map(t => t.querySelectorAll('.trail-step').length)")
        tty = await c.js("document.querySelector('.term-drawer:not(.is-think) .term-tab-count')?.textContent ?? '0'")
        think = await c.js("document.querySelector('.is-think .term-tab-count')?.textContent ?? '0'")
        print(f"    after reload: inputs={heads} steps per turn={steps} tty={tty} think={think}")
        (ok if heads == 2 else fail)("both inputs are there after a reload")
        (ok if all(n > 0 for n in steps[:2]) else fail)("each turn keeps its steps after a reload")
        (ok if int(tty) > 0 else fail)("the terminal history is rebuilt after a reload")
        (ok if int(think) > 0 else fail)("the thinking history is rebuilt after a reload")

        # The steps are part of the page: they must not be pressed into a fixed-height frame with
        # a scrollbar of their own.
        box = await c.js("""[...document.querySelectorAll('main .activity-trail')].map(t => ({
          h: Math.round(t.getBoundingClientRect().height), content: t.scrollHeight, client: t.clientHeight,
          overflow: getComputedStyle(t).overflowY, rows: t.querySelectorAll('.trail-step').length }))""")
        print(f"    step boxes: {box}")
        squashed = [x for x in box if x["overflow"] == "auto" and x["content"] > x["client"] + 1]
        (ok if not squashed else fail)("the steps are not squeezed into a fixed-height box of their own")

        print("== 3. the input of the turn on screen is pinned at the top ==")
        # Pad the answers so the chat scrolls well past one screen.
        await c.js("""document.querySelectorAll('main .msg.agent').forEach(m => {
          const pad = document.createElement('div'); pad.style.height = '1100px'; pad.className = 'probe-pad'; m.appendChild(pad) })""")
        await asyncio.sleep(0.3)
        pin = r"""(() => {
          const main = document.querySelector('main'); const top = main.getBoundingClientRect().top;
          const heads = [...main.querySelectorAll('.turn-head')];
          const at = heads.map(h => Math.round(h.getBoundingClientRect().top - top));
          // The pinned one sits at the top edge (inside <main>'s padding) while its own turn has
          // already scrolled above it; one that went off the top with its turn does not count.
          const pinned = heads.findIndex(h => { const r = h.getBoundingClientRect(); const t = h.parentElement.getBoundingClientRect();
            return r.top - top >= 0 && r.top - top < 40 && t.top < r.top - 2 });
          return { at, pinned };
        })()"""
        # The turn's position in the scroll content, measured from the scroller (offsetTop is
        # relative to the column, not to <main>).
        second_top = await c.js("""(() => { const m = document.querySelector('main');
          return Math.round(document.querySelectorAll('main .turn')[1].getBoundingClientRect().top
                            - m.getBoundingClientRect().top + m.scrollTop) })()""")
        # Scrolled like a reader: a wheel UP is what takes the chat off its end, and a scroll set
        # without it is pulled back to the end by the follower (which is the intended behaviour).
        await scroll_to(c, second_top + 600)
        await asyncio.sleep(0.4)
        a = await c.js(pin)
        await c.shot(f"{SHOTS}/4-pinned-second.png")
        await scroll_to(c, second_top - 500)
        await asyncio.sleep(0.4)
        b = await c.js(pin)
        await c.shot(f"{SHOTS}/5-pinned-first.png")
        print(f"    inside turn 2: {a}   above it: {b}")
        (ok if a["pinned"] == 1 else fail)("inside the second turn, the second input is pinned")
        (ok if b["pinned"] == 0 else fail)("scrolled above it, the first input takes its place")
        await c.js("document.querySelectorAll('.probe-pad').forEach(p => p.remove())")

        print("== 4. the ⋯ menu goes back ==")
        await c.js("document.querySelector('main').scrollTop = 1e9")
        await asyncio.sleep(0.3)
        await c.js("document.querySelectorAll('main .cp-btn')[1].click()")
        await asyncio.sleep(0.3)
        items = await c.js("[...document.querySelectorAll('.cp-menu button')].map(b => b.textContent.trim() + (b.disabled ? ' [disabled]' : ''))")
        print(f"    menu: {items}")
        await c.shot(f"{SHOTS}/6-menu.png")
        (ok if len(items) == 2 and not any("disabled" in i for i in items) else fail)("the menu offers both ways back")
        # Armed on the first click, applied on the second.
        await c.js("document.querySelectorAll('.cp-menu button')[1].click()")
        await asyncio.sleep(0.2)
        armed = await c.js("document.querySelector('.cp-menu button.is-armed')?.textContent ?? ''")
        await c.shot(f"{SHOTS}/7-armed.png")
        (ok if "again" in armed else fail)(f"the destructive item asks to be confirmed: {armed!r}")
        await c.js("document.querySelector('.cp-menu button.is-armed').click()")
        await until(c, "document.querySelectorAll('main .turn-head').length === 1", 15)
        await asyncio.sleep(1.0)
        heads = await c.js("document.querySelectorAll('main .turn-head').length")
        composer = await c.js("document.querySelector('form textarea').value")
        now = report()
        print(f"    inputs={heads} composer={composer!r} report.txt={now!r}")
        (ok if heads == 1 else fail)("the conversation went back to before the second input")
        (ok if composer == "write the report again" else fail)("the input is back in the composer, ready to edit")
        # The checkpoint of input 2 is the state it was SENT into: the hand edit came before it,
        # so it stays, and what input 2 wrote is gone.
        (ok if now == before_two else fail)("the files are exactly what they were when input 2 was sent")
        await c.shot(f"{SHOTS}/8-after-restore.png")

        cps = api("/v1/sessions/default/checkpoints")["checkpoints"]
        (ok if len(cps) == 1 else fail)(f"the gateway keeps {len(cps)} checkpoint(s)")

    print("VERDICT: " + ("ok" if failures == 0 else f"FAILED ({failures})"))
    return 1 if failures else 0


if __name__ == "__main__":
    os.makedirs(SHOTS, exist_ok=True)
    start_browser()
    page = [t for t in fetch_json(f"http://127.0.0.1:{PORT}/json/list", timeout=5) if t.get("type") == "page"][0]
    sys.exit(asyncio.run(main(page["webSocketDebuggerUrl"])))
