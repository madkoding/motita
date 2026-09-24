// The starlight interface.
//
// Three rules shape this file, and each one is a decision rather than a style:
//
//  1. It talks ONLY to its own origin, with relative paths. The page is served by the gateway
//     itself, so /v1/... is the same server - which is why there is no CORS anywhere and no
//     second address to configure. A hard-coded host would be wrong on the next machine.
//  2. It stores NOTHING. The credential the server hands out is an HttpOnly cookie the script
//     cannot even read; the token arrives in the URL fragment, is traded for that cookie once,
//     and is then erased from the address bar.
//  3. It resumes. A phone that loses signal must pick the turn up where it left it, so every
//     event id is remembered and the stream is re-requested from there.
//
// The stream from a POST /task is read directly from the response body, not via EventSource.
// EventSource cannot POST, and the response to the POST IS the stream — so the two are one
// request, not two. EventSource is used only for RECONNECTION when a live stream drops, because
// that is a GET and the gateway replays from the last sequence number the client saw.
'use strict';

// ─── Holographic tilt scene with doodle cats ────────────────────────────────
//
// The page is a perspective scene: three layers at different depths shift in
// paralaje as the mouse moves, like a Pokemon card tilted in the light. The
// deepest layer is a faint glow; the middle layer is a DENSE pattern of doodle
// cats, balls of yarn, paw prints, fish and hearts — like a WhatsApp doodle
// background — tiled across the whole viewport; the front layer is the UI.
// The cat layer drifts further than the UI layer, producing the 3D effect.
(function initTiltScene() {
  const scene = document.getElementById('scene');
  const catsLayer = document.getElementById('layer-cats');
  if (!scene || !catsLayer) return;

  // A doodle tile: many small cat-themed SVG shapes packed into a 200x200 tile
  // that repeats to fill the layer. This is the "lleno de doodles" part — the
  // pattern is dense, like a WhatsApp background, not 7 scattered cats.
  //
  // Each shape is a simple line doodle: cat faces, balls of yarn, paw prints,
  // fish, hearts, stars. They use stroke="currentColor" so the CSS --cat-line
  // colour drives everything.
  const tile = '<svg xmlns="http' + '://' + 'www.w3.org/2000/svg" width="200" height="200" viewBox="0 0 200 200">'
    // Cat face (top-left)
    + '<g transform="translate(20,20) scale(0.7)" stroke="currentColor" fill="none" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">'
    + '<path d="M5 30 Q0 20 3 10 L0 3 L12 10 Q20 6 28 10 L40 3 L37 10 Q42 20 37 30 Z"/>'
    + '<circle cx="15" cy="18" r="1.5" fill="currentColor" stroke="none"/>'
    + '<circle cx="27" cy="18" r="1.5" fill="currentColor" stroke="none"/>'
    + '<path d="M19 24 L21 26 L23 24"/>'
    + '<path d="M37 30 Q44 28 46 22"/>'
    + '</g>'
    // Ball of yarn (top-right)
    + '<g transform="translate(130,15) scale(0.8)" stroke="currentColor" fill="none" stroke-width="1.2">'
    + '<circle cx="25" cy="25" r="18"/>'
    + '<path d="M10 25 Q25 10 40 25 Q25 40 10 25"/>'
    + '<path d="M25 10 Q40 25 25 40 Q10 25 25 10"/>'
    + '<path d="M8 18 L42 32"/>'
    + '<path d="M8 32 L42 18"/>'
    + '</g>'
    // Paw print (mid-left)
    + '<g transform="translate(10,80) scale(0.6)" stroke="currentColor" fill="currentColor" stroke-width="1">'
    + '<ellipse cx="20" cy="28" rx="10" ry="7"/>'
    + '<circle cx="8" cy="14" r="4"/>'
    + '<circle cx="18" cy="8" r="4"/>'
    + '<circle cx="28" cy="8" r="4"/>'
    + '<circle cx="36" cy="14" r="4"/>'
    + '</g>'
    // Fish (mid-right)
    + '<g transform="translate(120,75) scale(0.7)" stroke="currentColor" fill="none" stroke-width="1.5" stroke-linecap="round">'
    + '<path d="M5 20 Q15 10 30 15 Q40 12 45 20 Q40 28 30 25 Q15 30 5 20 Z"/>'
    + '<path d="M45 20 L52 14 L52 26 Z"/>'
    + '<circle cx="12" cy="18" r="1.5" fill="currentColor" stroke="none"/>'
    + '</g>'
    // Heart (bottom-left)
    + '<g transform="translate(15,140) scale(0.5)" stroke="currentColor" fill="none" stroke-width="1.8" stroke-linecap="round">'
    + '<path d="M30 50 Q10 35 10 22 Q10 10 22 10 Q28 10 30 18 Q32 10 38 10 Q50 10 50 22 Q50 35 30 50 Z"/>'
    + '</g>'
    // Small cat curled (bottom-right)
    + '<g transform="translate(120,130) scale(0.6)" stroke="currentColor" fill="none" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">'
    + '<path d="M35 40 Q20 42 15 30 Q12 20 20 15 L15 8 L25 15 Q35 12 38 22 Q42 30 35 40 Z"/>'
    + '<circle cx="28" cy="25" r="1.2" fill="currentColor" stroke="none"/>'
    + '<path d="M32 28 L34 30" fill="none"/>'
    + '<path d="M35 40 Q40 38 42 32" fill="none"/>'
    + '</g>'
    // Star (centre)
    + '<g transform="translate(85,45) scale(0.4)" stroke="currentColor" fill="none" stroke-width="2" stroke-linecap="round">'
    + '<path d="M30 5 L36 22 L54 22 L40 33 L46 50 L30 40 L14 50 L20 33 L6 22 L24 22 Z"/>'
    + '</g>'
    // Small ball of yarn (centre-bottom)
    + '<g transform="translate(80,110) scale(0.5)" stroke="currentColor" fill="none" stroke-width="1.2">'
    + '<circle cx="20" cy="20" r="14"/>'
    + '<path d="M8 20 Q20 8 32 20 Q20 32 8 20"/>'
    + '<path d="M20 8 Q32 20 20 32 Q8 20 20 8"/>'
    + '</g>'
    // Tiny cat face (bottom-centre)
    + '<g transform="translate(70,160) scale(0.45)" stroke="currentColor" fill="none" stroke-width="1.5" stroke-linecap="round">'
    + '<path d="M5 25 Q0 15 3 8 L0 2 L10 8 Q15 5 20 8 L30 2 L27 8 Q32 15 27 25 Z"/>'
    + '<circle cx="10" cy="15" r="1" fill="currentColor" stroke="none"/>'
    + '<circle cx="20" cy="15" r="1" fill="currentColor" stroke="none"/>'
    + '<path d="M13 19 L15 21 L17 19"/>'
    + '</g>'
    // Paw print small (top-centre)
    + '<g transform="translate(75,5) scale(0.4)" stroke="currentColor" fill="currentColor" stroke-width="1">'
    + '<ellipse cx="20" cy="28" rx="10" ry="7"/>'
    + '<circle cx="8" cy="14" r="4"/>'
    + '<circle cx="18" cy="8" r="4"/>'
    + '<circle cx="28" cy="8" r="4"/>'
    + '<circle cx="36" cy="14" r="4"/>'
    + '</g>'
    // Fish small (bottom-right corner)
    + '<g transform="translate(155,165) scale(0.4)" stroke="currentColor" fill="none" stroke-width="1.5" stroke-linecap="round">'
    + '<path d="M5 20 Q15 10 30 15 Q40 12 45 20 Q40 28 30 25 Q15 30 5 20 Z"/>'
    + '<path d="M45 20 L52 14 L52 26 Z"/>'
    + '</g>'
    + '</svg>';

  // Set the tile as a repeating background on the cats layer. The SVG is embedded
  // as a data URI so there are zero image requests.
  const encoded = encodeURIComponent(tile).replace(/'/g, '%27').replace(/"/g, '%22');
  catsLayer.style.backgroundImage = "url(\"data:image/svg+xml;charset=utf-8," + encoded + "\")";
  catsLayer.style.backgroundRepeat = 'repeat';
  catsLayer.style.backgroundSize = '180px 180px';

  // Tilt: each layer shifts by its depth factor times the mouse offset from
  // centre. The cat layer (depth 0.06) moves more than the UI layer (depth 0.0),
  // so the cats appear to float between the viewer and the interface.
  const layers = scene.querySelectorAll('.layer');
  let targetX = 0, targetY = 0, currentX = 0, currentY = 0;

  window.addEventListener('mousemove', (e) => {
    targetX = (e.clientX / window.innerWidth - 0.5) * 2;
    targetY = (e.clientY / window.innerHeight - 0.5) * 2;
  });
  window.addEventListener('mouseleave', () => {
    targetX = 0;
    targetY = 0;
  });

  function animate() {
    // Smooth interpolation so the scene drifts rather than snaps.
    currentX += (targetX - currentX) * 0.06;
    currentY += (targetY - currentY) * 0.06;

    for (const layer of layers) {
      const depth = parseFloat(layer.dataset.depth || '0');
      const tx = -currentX * depth * 100;
      const ty = -currentY * depth * 100;
      // A slight rotateX/Y sells the 3D — the perspective on #scene does the rest.
      const rx = currentY * depth * 8;
      const ry = -currentX * depth * 8;
      layer.style.transform = 'translate3d(' + tx + 'px, ' + ty + 'px, 0) rotateX(' + rx + 'deg) rotateY(' + ry + 'deg)';
    }
    requestAnimationFrame(animate);
  }
  animate();
})();

(function () {
  const $ = (id) => document.getElementById(id);
  const conversation = $('conversation');
  const state = $('state');
  const form = $('composer');
  const task = $('task');
  const approval = $('approval');

  const session = 'default';

  // lastId is where the stream resumes from. It is the whole of the reconnection strategy: the
  // gateway numbers every event, and asks for a replay from a sequence number rather than
  // replaying everything.
  let lastId = 0;
  let running = false;
  let reconnectTimer = null;

  // activity is the transient block that shows what the agent is doing. It is replaced by each
  // progress event, and removed when the run finishes — the same shape the TUI's pending block
  // has, and the reason nothing is duplicated: the result goes in its own block, and the
  // activity block is cleared rather than left as a second copy of the answer.
  let activity = null;

  function say(text, cls) {
    const el = document.createElement('div');
    el.className = 'msg ' + (cls || 'agent');
    el.textContent = text;
    conversation.appendChild(el);
    conversation.scrollTop = conversation.scrollHeight;
    return el;
  }

  // showActivity replaces the transient activity line. The TUI overwrites the pending block with
  // each progress line; this is the same idea, kept in one element that is reused for every
  // progress event and removed when the run ends.
  function showActivity(text, kind) {
    if (!activity) {
      activity = document.createElement('div');
      conversation.appendChild(activity);
    }
    activity.className = 'msg activity' + (kind ? ' ' + kind : '');
    activity.textContent = text;
    conversation.scrollTop = conversation.scrollHeight;
  }

  function clearActivity() {
    if (activity) {
      activity.remove();
      activity = null;
    }
  }

  function setState(text, bad) {
    state.textContent = text;
    state.classList.toggle('bad', !!bad);
    state.classList.toggle('working', text === 'working' || text === 'reconnecting');
  }

  // api is the one place a request is built, so the credential handling and the error shape are
  // decided once. The cookie is sent automatically by the browser: nothing here sets a header.
  async function api(path, options) {
    const res = await fetch(path, Object.assign({ credentials: 'same-origin' }, options || {}));
    if (res.status === 401) {
      throw new Error('unauthorised');
    }
    return res;
  }

  // exchange trades the URL fragment for the cookie.
  //
  // The fragment is never sent to the server by the browser, which is exactly why the token goes
  // there: it does not land in a request line, a proxy log or a Referer. Once traded, it is
  // erased from the address bar and from the history entry, so it is not sitting in a screenshot
  // or a pasted bug report either.
  async function exchange() {
    const hash = window.location.hash || '';
    const marker = hash.indexOf('t=');
    if (marker === -1) {
      return false;
    }
    const token = hash.slice(marker + 2);
    if (!token) {
      return false;
    }
    const res = await fetch('/v1/webui/session', {
      method: 'POST',
      credentials: 'same-origin',
      headers: { Authorization: 'Bearer ' + token }
    });
    // Erased whatever the answer was: a token that did not work is not one to keep in history.
    history.replaceState(null, '', window.location.pathname + window.location.search);
    return res.ok;
  }

  // transcript paints the conversation that already exists, so opening the page on a machine
  // where a turn already ran does not show a blank screen.
  async function transcript() {
    const res = await api('/v1/sessions/' + session + '/messages');
    const data = await res.json();
    const messages = data.messages || [];
    if (messages.length === 0) {
      say('Nothing yet. Ask for something below.', 'agent kind');
      return;
    }
    for (const m of messages) {
      if (m.User) say(m.User, 'user');
      if (m.Agent) say(m.Agent, 'agent');
    }
  }

  // classifyProgress reads the prefix of a progress line and returns the activity kind.
  //
  // The agent emits progress lines with identifiable prefixes — "running:", "action:",
  // "validating", "planning", "analysing", "deciding" — and the kind drives the CSS class so
  // actions and status updates read differently. A line that matches no prefix is a generic
  // status line, which is the default and not a special case.
  function classifyProgress(text) {
    if (/^running:/.test(text)) return 'command';
    if (/^action:/.test(text)) return 'reasoning';
    if (/^(validating|validation)/.test(text)) return 'check';
    if (/^(planning|plan ready)/.test(text)) return 'plan';
    if (/^(analysing|understood)/.test(text)) return 'analysis';
    if (/^deciding/.test(text)) return 'reasoning';
    if (/^(task complete|synthesizing)/.test(text)) return 'synthesis';
    return null;
  }

  // dispatchEvent handles one parsed SSE event from either the POST response
  // or the reconnection stream. The event NAME drives the dispatch, not the
  // payload shape: the server sends named events (progress, done, error,
  // approval, attached), and guessing the type from the payload silently drops
  // anything that does not match the guess.
  function dispatchEvent(name, data, id) {
    if (id) {
      lastId = parseInt(id, 10) || lastId;
    }
    if (data === null) {
      return;
    }
    let payload;
    try {
      payload = JSON.parse(data);
    } catch (e) {
      say(data, 'agent');
      return;
    }
    switch (name) {
    case 'attached':
      // The preamble. Dropped events are reported, a pending approval is shown, and everything
      // else is metadata a browser client does not act on.
      if (payload.dropped > 0) {
        say('(' + payload.dropped + ' event(s) were not kept while nothing was listening)', 'agent kind');
      }
      if (payload.pending_approval) {
        ask(payload.pending_approval);
      }
      break;
    case 'progress':
      // A line of the turn as it happens. It is shown as a TRANSIENT activity line — the same
      // lines the TUI shows as the run advances — NOT as a permanent message. The result goes in
      // the done event, and painting both would duplicate the answer.
      showActivity(payload.text || '', classifyProgress(payload.text || ''));
      break;
    case 'approval':
      clearActivity();
      ask(payload);
      break;
    case 'done':
      // The run finished. The activity block is cleared — the result is the answer, not the last
      // status line — and the result goes in its own agent message.
      clearActivity();
      if (payload.result) {
        say(payload.result, 'agent');
      }
      finish();
      break;
    case 'error':
      clearActivity();
      if (payload.error) {
        say(payload.error, 'agent error');
      }
      finish();
      break;
    // Unknown events are ignored rather than rendered: a future server may add one, and a
    // client that crashes on an unrecognised name is worse than one that silently skips it.
    }
  }

  // parseFrame reads one SSE frame (the text between two blank lines) and returns its parts.
  //
  // Comment lines (starting with ':') are dropped: the keepalive is a comment, and it carries no
  // data or event name. A frame that is only a comment returns data=null and is not dispatched.
  function parseFrame(raw) {
    const lines = raw.split('\n');
    let id = null;
    let event = null;
    let data = null;
    for (const line of lines) {
      if (line.startsWith('id: ')) id = line.slice(4).trim();
      else if (line.startsWith('event: ')) event = line.slice(7).trim();
      else if (line.startsWith('data: ')) data = line.slice(6);
    }
    return { id: id, event: event, data: data };
  }

  // readStream consumes an SSE response body and dispatches each frame as it arrives.
  //
  // The POST to /task returns the stream in its response body: this function reads that body
  // chunk by chunk, splits it into SSE frames on the blank-line boundary, and dispatches each
  // one. This is the same thing EventSource does, except it works with a POST response and does
  // not auto-reconnect — reconnection is handled separately when a live stream drops.
  async function readStream(res) {
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    for (;;) {
      const { done, value } = await reader.read();
      if (done) {
        break;
      }
      buffer += decoder.decode(value, { stream: true });
      // SSE frames are separated by a blank line. The buffer may carry partial frames across
      // chunk boundaries, so everything up to the last complete boundary is dispatched and the
      // rest is kept for the next chunk.
      let idx;
      while ((idx = buffer.indexOf('\n\n')) !== -1) {
        const frame = parseFrame(buffer.slice(0, idx));
        buffer = buffer.slice(idx + 2);
        if (frame.data !== null) {
          dispatchEvent(frame.event || 'message', frame.data, frame.id);
        }
      }
    }
    // Flush whatever remained in the buffer when the stream closed.
    if (buffer.trim()) {
      const frame = parseFrame(buffer);
      if (frame.data !== null) {
        dispatchEvent(frame.event || 'message', frame.data, frame.id);
      }
    }
  }

  // ask shows an approval, with the command WHOLE.
  //
  // The command is what is being approved. Truncating it, or reflowing it, changes what the user
  // is agreeing to - so it goes into a <pre> that preserves whitespace and shows every character.
  function ask(pending) {
    approval.textContent = '';
    const title = document.createElement('h2');
    title.textContent = pending.question || pending.reason || 'This needs your approval';
    const command = document.createElement('pre');
    command.textContent = pending.command || '';
    const allow = document.createElement('button');
    allow.className = 'allow';
    allow.textContent = 'Run it';
    const deny = document.createElement('button');
    deny.className = 'deny';
    deny.textContent = 'No';

    function answer(yes) {
      approval.hidden = true;
      api('/v1/sessions/' + session + '/runs/approval', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: pending.id, approved: yes })
      }).catch(() => setState('could not answer the approval', true));
    }
    allow.addEventListener('click', () => answer(true));
    deny.addEventListener('click', () => answer(false));

    approval.appendChild(title);
    approval.appendChild(command);
    approval.appendChild(allow);
    approval.appendChild(deny);
    approval.hidden = false;
  }

  function finish() {
    running = false;
    clearActivity();
    setState('ready');
    task.disabled = false;
    form.querySelector('button').disabled = false;
  }

  // followReconnect reattaches to a dropped stream.
  //
  // It is ONLY called when a live stream from the POST response dropped mid-run. The gateway
  // still has the run and replays from lastId, so the client picks up where it left off. If the
  // run finished while the client was away, the endpoint returns 404 — the run is no longer
  // current — and the transcript is refreshed instead of looping forever.
  async function followReconnect() {
    let res;
    try {
      res = await api('/v1/sessions/' + session + '/events?from=' + lastId);
    } catch (e) {
      if (running) {
        setState('reconnecting', true);
        reconnectTimer = setTimeout(followReconnect, 1000);
      }
      return;
    }
    if (res.status === 404) {
      // The run is no longer in progress: it finished while the client was disconnected. The
      // final event may not have been seen, so the transcript is refreshed to show the result
      // rather than leaving the page on "reconnecting" forever.
      if (running) {
        finish();
        await transcript();
      }
      return;
    }
    if (!res.ok) {
      if (running) {
        setState('reconnecting', true);
        reconnectTimer = setTimeout(followReconnect, 1000);
      }
      return;
    }
    try {
      await readStream(res);
    } catch (e) {
      // The stream broke mid-read. Reconnect if the run is still going.
    }
    // The stream ended. If the run is still marked as running, the connection dropped rather
    // than the run finishing — reconnect to pick up the rest.
    if (running) {
      setState('reconnecting', true);
      reconnectTimer = setTimeout(followReconnect, 1000);
    }
  }

  async function submit(text) {
    say(text, 'user');
    running = true;
    setState('working');
    task.disabled = true;
    form.querySelector('button').disabled = true;
    let res;
    try {
      res = await api('/v1/sessions/' + session + '/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task: text })
      });
    } catch (e) {
      setState('the gateway refused the turn', true);
      finish();
      return;
    }
    if (!res.ok) {
      let msg = 'the gateway refused the turn';
      try {
        const err = await res.json();
        if (err.error) msg = err.error;
      } catch (e) { /* keep the generic message */ }
      setState(msg, true);
      finish();
      return;
    }
    // The response IS the stream: read it as SSE. Each event is dispatched as it arrives, so
    // the user sees the turn unfold line by line — the same lines the TUI shows, because the
    // gateway streams the same progress events to both.
    try {
      await readStream(res);
    } catch (e) {
      // The stream broke. Reconnect if the run is still going.
    }
    if (running) {
      setState('reconnecting', true);
      reconnectTimer = setTimeout(followReconnect, 1000);
    }
  }

  form.addEventListener('submit', (e) => {
    e.preventDefault();
    const text = task.value.trim();
    if (!text || running) {
      return;
    }
    task.value = '';
    submit(text);
  });

  async function start() {
    let ok = false;
    try {
      ok = await exchange();
    } catch (e) {
      ok = false;
    }
    try {
      // The transcript is the real test of whether the credential works: it is authenticated, so
      // a page that can read it is a page that can drive the agent.
      await transcript();
      setState('ready');
    } catch (e) {
      if (!ok) {
        setState('not connected', true);
        // The token is NOT a setting the reader forgot to make, and the message has to say so:
        // "requires a token" reads as "you were supposed to configure one", which sends people
        // looking through the configuration for a key that is not there - the gateway mints the
        // token itself on first start. What they need is the link, so that is what is named, along
        // with the reason the plain address cannot work.
        say('This page needs the link `starlight gateway start` printed, not the address on its own. ' +
            'The gateway generates its token the first time it starts - there is nothing to set up. ' +
            'The token travels in the `#t=...` fragment, and a browser never sends a fragment to the ' +
            'server, which is why opening this address without it arrives here with no credential. ' +
            'Run `starlight gateway start` again to print the link.', 'agent kind');
        return;
      }
      setState('not connected', true);
    }
  }

  start();
})();