// Drives headless Chrome over raw CDP for verify-resume.sh. No framework: the `ws` package the
// Hermes tree already ships is enough.
const WebSocket = require(process.env.WS_MODULE)
const http = require('http')
const fs = require('fs')
const { CDP_PORT, BASE, TOKEN, SID, OUT } = process.env

const get = url => new Promise((res, rej) => http.get(url, r => {
  let b = ''; r.on('data', d => b += d); r.on('end', () => res(JSON.parse(b)))
}).on('error', rej))
const sleep = ms => new Promise(r => setTimeout(r, ms))

;(async () => {
  const tabs = await get(`http://127.0.0.1:${CDP_PORT}/json/list`)
  const ws = new WebSocket(tabs.find(t => t.type === 'page').webSocketDebuggerUrl)
  await new Promise(r => ws.on('open', r))
  let id = 0; const pending = new Map()
  ws.on('message', m => { const j = JSON.parse(m); if (pending.has(j.id)) { pending.get(j.id)(j); pending.delete(j.id) } })
  const call = (method, params = {}) => new Promise(r => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
  const js = async expr => (await call('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })).result.result.value

  await call('Page.enable'); await call('Network.enable')
  await call('Network.setCacheDisabled', { cacheDisabled: true })
  // The token trades for a cookie; `about:blank` first, because a fragment-only navigation does not reload.
  await call('Page.navigate', { url: `${BASE}/#t=${TOKEN}` }); await sleep(2500)
  await call('Page.navigate', { url: 'about:blank' })
  await call('Page.navigate', { url: `${BASE}/#t=${TOKEN}` }); await sleep(2500)
  await js(`(async()=>{for(const r of await navigator.serviceWorker.getRegistrations())await r.unregister();for(const k of await caches.keys())await caches.delete(k)})()`)
  await call('Page.reload', { ignoreCache: true }); await sleep(2500)

  // Open the session the way a reader does: click its row in the sidebar.
  const clicked = await js(`(()=>{const rows=[...document.querySelectorAll('.session-row')];const r=rows.find(x=>x.textContent.includes('Resume check'));if(r){r.click();return true}return rows.map(x=>x.textContent.slice(0,30))})()`)
  if (clicked !== true) console.log('FAIL open-session rows=' + JSON.stringify(clicked))
  await sleep(2500)
  const text = await js('document.body.innerText')
  fs.writeFileSync(`${OUT}/page.txt`, text)
  await call('Page.captureScreenshot', { format: 'png' }).then(r => fs.writeFileSync(`${OUT}/resume.png`, Buffer.from(r.result.data, 'base64')))

  const working = await js(`(()=>{const s=[...document.querySelectorAll('.session-row')].find(x=>x.textContent.includes('Resume check'));return !!s && !!s.querySelector('.session-spinner.is-running')})()`)
  console.log((working ? 'PASS' : 'FAIL') + ' running-indicator')
  console.log((text.includes('SCHEMA-PATCH-MARKER') ? 'PASS' : 'FAIL') + ' earlier-work-visible')
  const times = text.split('SCHEMA-PATCH-MARKER').length - 1
  console.log((times <= 1 ? 'PASS' : 'FAIL') + ` not-drawn-twice (${times}x)`)
  ws.close(); process.exit(0)
})().catch(e => { console.log('FAIL driver ' + e.message); process.exit(0) })
