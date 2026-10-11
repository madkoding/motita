"""Measure the read-only composer on a real merged session, in a real browser.

The property this asserts is one the markup alone cannot: a session whose work
has been integrated must READ as read-only where the user actually types. So the
probe drives the page the way a person does - pick the merged session in the
sidebar, then look at the composer - and measures:

  * the "Merged — Read Only" tag is PRESENT and VISIBLE in the composer;
  * the textarea is disabled, so no keystroke can start a turn;
  * the send button is disabled;
  * the placeholder says why.

Usage: GATEWAY_URL=... GATEWAY_STATE=<token file> ./cdp_readonly.py
"""
import asyncio, base64, json, os, sys, tempfile

sys.path.insert(0, os.path.dirname(__file__))
from cdp_http import fetch_json  # noqa: E402
from cdp_spinner import CDP  # noqa: E402

BASE = os.environ["GATEWAY_URL"]
TOKEN = open(os.environ["GATEWAY_STATE"]).read().strip()
SHOTS = os.environ.get("SHOTS_DIR") or tempfile.mkdtemp(prefix="motita-readonly-")
failures = 0


def check(cond, msg):
    global failures
    if cond:
        print("  ok   " + msg)
    else:
        failures += 1
        print("  FAIL " + msg)


def find_merged_session():
    data = fetch_json(BASE + "/v1/sessions", headers={"Authorization": "Bearer " + TOKEN}, timeout=5)
    for s in data.get("sessions", []):
        if s.get("merged"):
            return s
    return None


# Pick the merged session in the sidebar, exactly as a click would. Rows carry
# no id, so the match is on the title the row draws - the same string the user
# would read to find it.
PICK = """(async (title) => {
  const rows = [...document.querySelectorAll('.session-row')];
  const row = rows.find(r => (r.textContent || '').includes(title));
  if (!row) return {error: 'row not found', seen: rows.map(r => (r.textContent || '').slice(0, 40))};
  row.click();
  await new Promise(r => setTimeout(r, 900));
  return {clicked: true};
})"""

# Read the composer as the user sees it.
COMPOSER = """(() => {
  const form = [...document.querySelectorAll('form')].find(f => f.querySelector('#task'));
  if (!form) return {error: 'no composer'};
  const ta = form.querySelector('#task');
  const btn = form.querySelector('button[type=submit]');
  const tag = [...form.querySelectorAll('span')].find(s => (s.textContent || '').includes('Read Only'));
  const tagCS = tag ? getComputedStyle(tag) : null;
  const tagR = tag ? tag.getBoundingClientRect() : null;
  return {
    tagText: tag ? tag.textContent.trim() : null,
    tagVisible: !!tag && tagCS.display !== 'none' && tagCS.visibility !== 'hidden'
                && parseFloat(tagCS.opacity) > 0.1 && tagR.width > 0 && tagR.height > 0,
    textareaDisabled: ta.disabled,
    placeholder: ta.placeholder,
    sendDisabled: btn ? btn.disabled : null,
  };
})()"""


async def main(ws_url):
    import websockets
    os.makedirs(SHOTS, exist_ok=True)
    async with websockets.connect(ws_url, max_size=50 * 1024 * 1024) as ws:
        c = CDP(ws)
        await c.call("Page.enable")
        await c.call("Runtime.enable")
        await c.call("Emulation.setDeviceMetricsOverride", width=1280, height=900,
                     deviceScaleFactor=1, mobile=False)
        await c.call("Page.navigate", url="about:blank")
        await c.call("Page.navigate", url=f"{BASE}/#t={TOKEN}")
        await asyncio.sleep(3)
        # A service worker would serve a stale bundle; drop it.
        await c.js("(async () => { for (const r of await navigator.serviceWorker.getRegistrations()) await r.unregister(); return true })()")
        await c.call("Page.navigate", url=f"{BASE}/#t={TOKEN}")
        await asyncio.sleep(2.5)

        s = find_merged_session()
        if s is None:
            print("  SKIP no merged session exists to measure")
            return 0
        print(f"  merged session: {s['id']} sha={s.get('merged_sha')}")

        res = await c.js(f"({PICK})({json.dumps(s['title'] or s['id'])})")
        check(isinstance(res, dict) and res.get("clicked"), f"the merged session is selectable: {res}")
        await asyncio.sleep(0.6)

        got = await c.js(COMPOSER)
        print("  composer:", json.dumps(got, ensure_ascii=False))
        if not isinstance(got, dict) or got.get("error"):
            check(False, f"the composer must exist: {got}")
            return failures
        check(got.get("tagVisible") is True,
              f"the composer shows a visible read-only tag (got {got.get('tagText')!r})")
        check(got.get("tagText") == "Merged — Read Only",
              f"the tag reads exactly 'Merged — Read Only' (got {got.get('tagText')!r})")
        check(got.get("textareaDisabled") is True, "the textarea is disabled")
        check(got.get("sendDisabled") is True, "the send button is disabled")
        check("read-only" in (got.get("placeholder") or "").lower(),
              f"the placeholder says why (got {got.get('placeholder')!r})")

        await c.shot(os.path.join(SHOTS, "readonly-composer.png"))
        print("  screenshot:", os.path.join(SHOTS, "readonly-composer.png"))
    return failures


if __name__ == "__main__":
    ws = sys.argv[1]
    sys.exit(1 if asyncio.run(main(ws)) else 0)
