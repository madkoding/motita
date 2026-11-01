// Drives verify-two-browsers.sh: two browser profiles on the same gateway. Browser A starts a
// session; browser B must see it appear in its list without a reload and open it. Then A asks a
// second question: B must show it while it runs, and show its answer when it ends.
const { chromium } = require("playwright");
const { BASE, TOKEN, OUT } = process.env;

const FIRST = "two-browser-check one: what is in the folder?";
const SECOND = "two-browser-check two: and what is in it now?";
let failed = 0;
const ok = (m) => console.log("  ok    " + m);
const bad = (m) => {
  console.log("  FAIL  " + m);
  failed++;
};

async function api(path) {
  const res = await fetch(BASE + path, { headers: { Authorization: "Bearer " + TOKEN } });
  return { status: res.status, body: res.ok ? await res.json() : null };
}

async function waitFor(fn, ms) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await fn().catch(() => false)) return true;
    await new Promise((r) => setTimeout(r, 250));
  }
  return false;
}

async function open(browser, name) {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  await page.goto(`${BASE}/#t=${TOKEN}`);
  const login = page.getByPlaceholder("Paste the gateway token…");
  // The token in the link is traded for a cookie; a page that still asks gets it typed in, again
  // if the first answer raced the trade.
  for (let i = 0; i < 4 && (await waitFor(() => login.isVisible(), 2000)); i++) {
    await login.fill(TOKEN);
    await page.getByRole("button", { name: "Connect" }).click();
    await waitFor(async () => !(await login.isVisible()), 4000);
  }
  await page.waitForSelector("#task:not([disabled])", { timeout: 20000 });
  await page.screenshot({ path: `${OUT}/${name}-open.png` });
  return page;
}

const bodyText = (page) => page.evaluate(() => document.body.innerText);
const count = async (page, text) => (await bodyText(page)).split(text).length - 1;

// running is the session with a turn in flight, or "".
async function running() {
  const list = (await api("/v1/sessions")).body;
  const all = (list && (list.sessions || list)) || [];
  const s = Array.isArray(all) ? all.find((x) => x.running) : null;
  return s ? s.id : "";
}

async function ask(page, text) {
  await page.fill("#task", text);
  await page.press("#task", "Enter");
  let id = "";
  await waitFor(async () => (id = await running()) !== "", 15000);
  return id;
}

async function waitEnd(id) {
  return waitFor(async () => (await api(`/v1/sessions/${id}/run`)).status !== 200, 120000);
}

// lastAnswer is the start of the newest answer of a session, as a page would draw it.
async function lastAnswer(id) {
  const msgs = (await api(`/v1/sessions/${id}/messages`)).body;
  const all = (msgs && msgs.messages) || [];
  const answerOf = (m) => String(m.agent || m.Agent || m.answer || "").trim();
  const last = [...all].reverse().find(answerOf);
  const text = last ? answerOf(last) : "";
  const line = text.split("\n").map((l) => l.replace(/[*_`#>|-]/g, "").trim()).find((l) => l.length > 12);
  return line ? line.slice(0, 30).trim() : "";
}

(async () => {
  const browser = await chromium.launch();
  try {
    const a = await open(browser, "a");
    const b = await open(browser, "b");

    const id = await ask(a, FIRST);
    if (id) ok("browser A started a session: " + id);
    else bad("no run started from browser A");
    await waitEnd(id);

    const row = b.locator("[data-testid=sidebar-sessions] .session-row").first();
    if (await waitFor(() => row.isVisible(), 15000)) {
      ok("the new session appeared in browser B's list without a reload");
      await row.click();
    } else {
      bad("browser B's list never showed the new session");
    }
    if (await waitFor(async () => (await bodyText(b)).includes(FIRST), 10000)) {
      ok("browser B opened it and shows the first question");
    } else {
      bad("browser B does not show the first question");
    }

    // The simulated model answers both questions alike, so the second answer is told apart by count.
    const firstAnswer = await lastAnswer(id);
    const before = firstAnswer ? await count(b, firstAnswer) : 0;
    const again = await ask(a, SECOND);
    if (again === id) ok("browser A asked again in the same session");
    else bad("the second question ran in " + JSON.stringify(again));
    if (await waitFor(async () => (await bodyText(b)).includes(SECOND), 15000)) {
      ok("browser B shows the second question while the turn runs");
    } else {
      bad("browser B never showed the second question while it ran");
    }
    await b.screenshot({ path: OUT + "/b-running.png" });

    if (await waitEnd(id)) ok("the turn ended");
    else bad("the turn did not end in two minutes");
    const probe = await lastAnswer(id);
    if (!probe) {
      bad("the transcript has no answer to look for");
    } else if (await waitFor(async () => (await count(b, probe)) > (probe === firstAnswer ? before : 0), 15000)) {
      ok("browser B shows the answer without a reload: " + JSON.stringify(probe));
    } else {
      bad("browser B does not show the answer " + JSON.stringify(probe));
    }
    await b.screenshot({ path: OUT + "/b-done.png" });
  } finally {
    await browser.close();
  }
  console.log(failed ? `${failed} check(s) failed` : "all checks passed");
  process.exit(failed ? 1 : 0);
})();
