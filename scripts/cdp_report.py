"""Probe the structured task report on a scratch gateway.

Driven by scripts/verify-report.sh. The simulated LLM answers the final phase with a report that
has every section, so what is asserted here is MEASURED on the rendered page:

  * the answer is a CARD (badge, tally, sections), not a Markdown blob;
  * the verdict, the counts and each list say what the report says (pass/skipped marks included);
  * nothing overflows the message, on a desktop width and on a 390px phone;
  * the card fits inside the chat column (no horizontal scroll).
"""
import asyncio, json, os, sys, tempfile

sys.path.insert(0, os.path.dirname(__file__))
from cdp_spinner import CDP  # noqa: E402
from cdp_checkpoints import send, until  # noqa: E402  (its module-level env is the same one)

BASE = os.environ["GATEWAY_URL"]
TOKEN = open(os.environ["GATEWAY_STATE"]).read().strip()
SHOTS = os.environ.get("SHOTS_DIR") or tempfile.mkdtemp(prefix="motita-report-")
failures = 0


def fail(msg):
    global failures
    failures += 1
    print("  FAIL " + msg)


def ok(msg):
    print("  ok   " + msg)


def check(cond, msg):
    (ok if cond else fail)(msg)


MEASURE = """(() => {
  const card = document.querySelector('.msg.agent .rpt');
  if (!card) return null;
  const msg = card.closest('.msg'), main = document.querySelector('main');
  const q = (s) => [...card.querySelectorAll(s)].map(e => e.textContent.trim());
  return {
    badge: card.querySelector('.rpt-badge')?.textContent,
    tally: q('.rpt-tally'),
    summaryHasBold: !!card.querySelector('.rpt-summary strong'),
    kinds: q('.rpt-kind'), paths: q('.rpt-path'),
    marks: q('.rpt-mark'), checks: q('.rpt-check'), evidence: q('.rpt-evidence'),
    risks: q('.rpt-risks li'), next: q('.rpt-next li'),
    sections: q('.rpt-sec h4'),
    passBg: getComputedStyle(card.querySelector('.rpt-checks .pass .rpt-mark')).backgroundColor,
    badgeColor: getComputedStyle(card.querySelector('.rpt-badge')).color,
    cardOverflowX: card.scrollWidth > card.clientWidth + 1,
    msgOverflowX: msg.scrollWidth > msg.clientWidth + 1,
    pageOverflowX: main.scrollWidth > main.clientWidth + 1,
    msgRight: msg.getBoundingClientRect().right, viewport: innerWidth,
    plainMarkdownToo: !!msg.querySelector(':scope > .md, :scope > p'),
  };
})()"""


async def frame_card(c):
    """Put the card on screen for the picture: the sidebar drawer closed, the toast dismissed, the
    card scrolled into view. A screenshot of a card that is out of frame proves nothing."""
    await c.js("""(() => {
      document.querySelector('button[aria-label="Close sidebar"]')?.click();
      document.querySelectorAll('[role=status] button, .toast button').forEach(b => b.click());
      document.querySelector('.msg.agent .rpt')?.scrollIntoView({block: 'center'});
    })()""")
    await asyncio.sleep(0.8)


async def main(ws_url):
    import websockets
    async with websockets.connect(ws_url, max_size=64 << 20) as ws:
        c = CDP(ws)
        await c.call("Page.enable")
        await c.call("Network.setCacheDisabled", cacheDisabled=True)
        await c.call("Emulation.setDeviceMetricsOverride", width=1280, height=760, deviceScaleFactor=1, mobile=False)
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

        print("== 1. a finished task is drawn as a report card ==")
        await send(c, "write the report")
        got = await until(c, "!!document.querySelector('.msg.agent .rpt')", 120)
        check(got, "a .rpt card appears in the answer")
        await asyncio.sleep(1.0)
        m = await c.js(MEASURE)
        if not m:
            fail("no card to measure")
        else:
            print("    measured:", json.dumps(m)[:700])
            check(m["badge"] == "Done", f"the verdict badge says {m['badge']!r}")
            check(any("1/2 checks passed" in t for t in m["tally"]), f"the check tally is {m['tally']}")
            check(any("1 change" in t for t in m["tally"]), "the change count is shown")
            check(m["summaryHasBold"], "the summary is still rendered as Markdown")
            check(m["kinds"] == ["new"] and m["paths"] == ["report.txt"], f"the change row: {m['kinds']} {m['paths']}")
            check(m["marks"] == ["✓", "–"], f"pass and skipped are told apart: {m['marks']}")
            check(m["evidence"][:1] == ["ANCHOR_OK"], f"the evidence is shown: {m['evidence']}")
            check(m["risks"] == ["the file is overwritten on every run"], f"risks: {m['risks']}")
            check(m["next"] == ["commit report.txt"], f"next steps: {m['next']}")
            check(m["sections"] == ["What changed", "How it was checked", "Worth knowing", "Next"], f"sections: {m['sections']}")
            check(m["passBg"] == "rgb(126, 231, 135)", f"a passed check is green: {m['passBg']}")
            check(m["badgeColor"] == "rgb(126, 231, 135)", f"the badge takes the verdict colour: {m['badgeColor']}")
            check(not m["cardOverflowX"] and not m["msgOverflowX"] and not m["pageOverflowX"], "no horizontal overflow on desktop")
        await frame_card(c)
        await c.shot(f"{SHOTS}/report-desktop.png")

        print("== 2. the same card on a phone ==")
        await c.call("Emulation.setDeviceMetricsOverride", width=390, height=800, deviceScaleFactor=2, mobile=True)
        await asyncio.sleep(1.0)
        m = await c.js(MEASURE)
        check(bool(m), "the card is still there at 390px")
        if m:
            check(not m["cardOverflowX"] and not m["msgOverflowX"] and not m["pageOverflowX"], "no horizontal overflow at 390px")
            check(m["msgRight"] <= m["viewport"] + 1, f"the message stays inside the screen ({m['msgRight']:.0f} <= {m['viewport']})")
        await frame_card(c)
        await c.shot(f"{SHOTS}/report-phone.png")

    print(f"VERDICT: {'FAILED' if failures else 'ok'} ({failures} failures)")
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    from cdp_http import fetch_json
    from cdp_spinner import start_browser, PORT
    os.makedirs(SHOTS, exist_ok=True)
    start_browser()
    page = [t for t in fetch_json(f"http://127.0.0.1:{PORT}/json/list", timeout=5) if t.get("type") == "page"][0]
    asyncio.run(main(page["webSocketDebuggerUrl"]))
