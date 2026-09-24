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

// ─── Holographic background ──────────────────────────────────────────────────
//
// A particle network on a <canvas> that reacts to the mouse: particles drift, connect with lines
// when they are close, and the whole field shifts toward the cursor. The colour shifts with depth
// — nearer particles are brighter — which is the "holographic" part. It is drawn procedurally on a
// canvas, so it costs zero bytes of assets and adapts to any screen.
(function initBackground() {
  const canvas = document.getElementById('bg');
  if (!canvas) return;
  const ctx = canvas.getContext('2d');
  let W = 0, H = 0, particles = [], mouse = { x: -999, y: -999 };

  function resize() {
    W = canvas.width = window.innerWidth;
    H = canvas.height = window.innerHeight;
  }
  resize();
  window.addEventListener('resize', resize);

  // Particle count scales with screen area but is capped: a netbook with 484 MB of RAM cannot
  // animate 500 particles, and the visual is the same with fewer.
  const count = Math.min(80, Math.floor((W * H) / 18000));
  for (let i = 0; i < count; i++) {
    particles.push({
      x: Math.random() * W,
      y: Math.random() * H,
      vx: (Math.random() - 0.5) * 0.3,
      vy: (Math.random() - 0.5) * 0.3,
      r: Math.random() * 1.5 + 0.5,
      // depth drives brightness: 0 = far (dim), 1 = near (bright).
      d: Math.random()
    });
  }

  window.addEventListener('mousemove', (e) => {
    mouse.x = e.clientX;
    mouse.y = e.clientY;
  });
  window.addEventListener('mouseleave', () => {
    mouse.x = -999;
    mouse.y = -999;
  });

  function draw() {
    ctx.clearRect(0, 0, W, H);

    // The accent colour is read from CSS so the canvas matches the theme (light/dark).
    const style = getComputedStyle(document.documentElement);
    const accent = style.getPropertyValue('--accent').trim() || '#4cc2ff';
    // Parse the hex into r/g/b for alpha work.
    const hex = accent.replace('#', '');
    const ar = parseInt(hex.slice(0, 2), 16);
    const ag = parseInt(hex.slice(2, 4), 16);
    const ab = parseInt(hex.slice(4, 6), 16);

    for (let i = 0; i < particles.length; i++) {
      const p = particles[i];

      // Mouse attraction: particles within 150px are pulled gently toward the cursor, which is
      // the "moves with the mouse" part. The force is small so the field drifts rather than snaps.
      const dx = mouse.x - p.x;
      const dy = mouse.y - p.y;
      const dist = Math.sqrt(dx * dx + dy * dy);
      if (dist < 150 && dist > 0) {
        p.vx += (dx / dist) * 0.02;
        p.vy += (dy / dist) * 0.02;
      }

      // Damping keeps the pull from accumulating into a slingshot.
      p.vx *= 0.99;
      p.vy *= 0.99;
      p.x += p.vx;
      p.y += p.vy;

      // Wrap around the edges so particles never leave the field.
      if (p.x < 0) p.x = W;
      if (p.x > W) p.x = 0;
      if (p.y < 0) p.y = H;
      if (p.y > H) p.y = 0;

      // Draw the particle: brightness scales with depth.
      const alpha = 0.2 + p.d * 0.5;
      ctx.beginPath();
      ctx.arc(p.x, p.y, p.r, 0, Math.PI * 2);
      ctx.fillStyle = 'rgba(' + ar + ',' + ag + ',' + ab + ',' + alpha + ')';
      ctx.fill();

      // Connect to nearby particles: the line's alpha falls off with distance, which is what
      // makes the network look like a network and not a star field.
      for (let j = i + 1; j < particles.length; j++) {
        const q = particles[j];
        const ldx = p.x - q.x;
        const ldy = p.y - q.y;
        const ld = Math.sqrt(ldx * ldx + ldy * ldy);
        if (ld < 120) {
          const lineAlpha = (1 - ld / 120) * 0.15;
          ctx.beginPath();
          ctx.moveTo(p.x, p.y);
          ctx.lineTo(q.x, q.y);
          ctx.strokeStyle = 'rgba(' + ar + ',' + ag + ',' + ab + ',' + lineAlpha + ')';
          ctx.lineWidth = 0.5;
          ctx.stroke();
        }
      }
    }
    requestAnimationFrame(draw);
  }
  draw();
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