// Drives verify-two-browsers.sh: two browser profiles on the same gateway. Browser A starts a
// session; browser B must see it appear in its list without a reload and open it. Then A asks a
// second question: B must show it while it runs, and show its answer when it ends.
const { chromium } = require("playwright");
const { BASE, TOKEN, OUT } = process.env;

const FIRST = "two-browser-check one: what is in the folder?";
const SECOND = "two-browser-check two: and what is in it now?";
const POLL_MS = 250;
const LOGIN_MS = 2000;
const LOGIN_TRIES = 4;
const CONNECT_MS = 4000;
const READY_MS = 20000;
const SEEN_MS = 15000;
const TURN_MS = 120000;
const HTTP_OK = 200;
const MIN_LINE = 12;
const PROBE_LEN = 30;
let failed = 0;

function check(passed, good, wrong) {
  if (passed) {
    console.info("  ok    " + good);
    return;
  }
  console.info("  FAIL  " + wrong);
  failed++;
}

async function api(path) {
  const res = await fetch(BASE + path, { headers: { Authorization: "Bearer " + TOKEN } });
  let body = null;
  if (res.ok) {
    body = await res.json();
  }
  return { status: res.status, body };
}

async function waitFor(fn, ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await fn().catch(() => false)) {
      return true;
    }
    await new Promise((r) => setTimeout(r, POLL_MS));
  }
  return false;
}

// login types the token in when the page still asks: the token in the link is traded for a
// cookie, and a first answer can race that trade.
async function login(page) {
  const field = page.getByPlaceholder("Paste the gateway token…");
  for (let i = 0; i < LOGIN_TRIES && (await waitFor(() => field.isVisible(), LOGIN_MS)); i++) {
    await field.fill(TOKEN);
    await page.getByRole("button", { name: "Connect" }).click();
    await waitFor(async () => !(await field.isVisible()), CONNECT_MS);
  }
}

async function open(browser, name) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  await page.goto(`${BASE}/#t=${TOKEN}`);
  await login(page);
  await page.waitForSelector("#task:not([disabled])", { timeout: READY_MS });
  await page.screenshot({ path: `${OUT}/${name}-open.png` });
  return page;
}

const bodyText = (page) => page.evaluate(() => document.body.innerText);
const count = async (page, text) => (await bodyText(page)).split(text).length - 1;
const shows = (page, text) => waitFor(async () => (await bodyText(page)).includes(text), SEEN_MS);

// running is the session with a turn in flight, or "".
async function running() {
  const list = (await api("/v1/sessions")).body || {};
  const all = list.sessions || [];
  const s = all.find((x) => x.running);
  return s?.id || "";
}

async function ask(page, text) {
  await page.fill("#task", text);
  await page.press("#task", "Enter");
  let id = "";
  await waitFor(async () => (id = await running()) !== "", SEEN_MS);
  return id;
}

function waitEnd(id) {
  return waitFor(async () => (await api(`/v1/sessions/${id}/run`)).status !== HTTP_OK, TURN_MS);
}

// lastAnswer is the start of the newest answer of a session, as a page would draw it.
async function lastAnswer(id) {
  const msgs = (await api(`/v1/sessions/${id}/messages`)).body || {};
  const answerOf = (m) => String(m.agent || m.Agent || m.answer || "").trim();
  const last = [...(msgs.messages || [])].reverse().find(answerOf);
  const lines = answerOf(last || {}).split("\n");
  const line = lines.map((l) => l.replace(/[*_`#>|-]/g, "").trim()).find((l) => l.length > MIN_LINE);
  return (line || "").slice(0, PROBE_LEN).trim();
}

// firstTurn: A starts a session, B finds it in its list and opens it.
async function firstTurn(a, b) {
  const id = await ask(a, FIRST);
  check(id, "browser A started a session: " + id, "no run started from browser A");
  await waitEnd(id);
  const row = b.locator("[data-testid=sidebar-sessions] .session-row").first();
  const listed = await waitFor(() => row.isVisible(), SEEN_MS);
  check(listed, "the new session appeared in browser B's list without a reload",
    "browser B's list never showed the new session");
  if (listed) {
    await row.click();
  }
  check(await shows(b, FIRST), "browser B opened it and shows the first question",
    "browser B does not show the first question");
  return id;
}

// secondTurn: A asks again; B shows the question while it runs and the answer when it ends. The
// simulated model answers both questions alike, so the second answer is told apart by count.
async function secondTurn(a, b, id) {
  const firstAnswer = await lastAnswer(id);
  let before = 0;
  if (firstAnswer) {
    before = await count(b, firstAnswer);
  }
  const again = await ask(a, SECOND);
  check(again === id, "browser A asked again in the same session", "the second question ran in " + again);
  check(await shows(b, SECOND), "browser B shows the second question while the turn runs",
    "browser B never showed the second question while it ran");
  await b.screenshot({ path: OUT + "/b-running.png" });
  check(await waitEnd(id), "the turn ended", "the turn did not end in two minutes");
  const probe = await lastAnswer(id);
  if (probe !== firstAnswer) {
    before = 0;
  }
  const seen = probe !== "" && (await waitFor(async () => (await count(b, probe)) > before, SEEN_MS));
  check(seen, "browser B shows the answer without a reload: " + JSON.stringify(probe),
    "browser B does not show the answer " + JSON.stringify(probe));
  await b.screenshot({ path: OUT + "/b-done.png" });
}

async function main() {
  const browser = await chromium.launch();
  try {
    const a = await open(browser, "a");
    const b = await open(browser, "b");
    const id = await firstTurn(a, b);
    await secondTurn(a, b, id);
  } finally {
    await browser.close();
  }
  if (failed) {
    console.info(`${failed} check(s) failed`);
    process.exit(1);
  }
  console.info("all checks passed");
}

main();
