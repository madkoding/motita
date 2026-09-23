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
'use strict';

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

  function say(text, cls) {
    const el = document.createElement('div');
    el.className = 'msg ' + (cls || 'agent');
    el.textContent = text;
    conversation.appendChild(el);
    conversation.scrollTop = conversation.scrollHeight;
    return el;
  }

  function setState(text, bad) {
    state.textContent = text;
    state.classList.toggle('bad', !!bad);
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

  // handleEvent paints one server-sent event. The gateway sends a preamble when a stream is
  // attached to a run already in flight, and dropping events is reported rather than hidden: a
  // transcript with a hole in it that does not say so is a lie.
  function handleEvent(raw) {
    const lines = raw.split('\n');
    let id = null;
    let data = null;
    for (const line of lines) {
      if (line.startsWith('id: ')) id = line.slice(4).trim();
      else if (line.startsWith('data: ')) data = line.slice(6);
    }
    if (id !== null && id !== '') {
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
    if (payload.dropped > 0) {
      say('(' + payload.dropped + ' event(s) were not kept while nothing was listening)', 'agent kind');
    }
    if (payload.result) {
      say(payload.result, 'agent');
      finish();
      return;
    }
    if (payload.text) {
      say(payload.text, 'agent');
    }
    if (payload.approval) {
      ask(payload.approval);
    }
  }

  // ask shows an approval, with the command WHOLE.
  //
  // The command is what is being approved. Truncating it, or reflowing it, changes what the user
  // is agreeing to - so it goes into a <pre> that preserves whitespace and shows every character.
  function ask(pending) {
    approval.textContent = '';
    const title = document.createElement('h2');
    title.textContent = pending.question || 'This needs your approval';
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
    setState('ready');
    task.disabled = false;
    form.querySelector('button').disabled = false;
  }

  // follow reads the event stream from lastId.
  //
  // EventSource cannot POST, which is why the turn is started with fetch and the stream is read
  // with EventSource afterwards: the run lives in the GATEWAY, not in the connection, so starting
  // it and watching it are two separate things that a client may do on two different connections.
  function follow() {
    const source = new EventSource('/v1/sessions/' + session + '/events?from=' + lastId);
    source.onmessage = (e) => {
      if (e.lastEventId) {
        lastId = parseInt(e.lastEventId, 10) || lastId;
      }
      handleEvent(e.data);
    };
    source.onerror = () => {
      source.close();
      if (running) {
        // The run outlived the connection - a phone that changed network, a proxy that timed
        // out. Reattach, and the gateway replays only what came after lastId.
        setState('reconnecting', true);
        setTimeout(follow, 1000);
      }
    };
  }

  async function submit(text) {
    say(text, 'user');
    running = true;
    setState('working');
    task.disabled = true;
    form.querySelector('button').disabled = true;
    try {
      await api('/v1/sessions/' + session + '/task', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ task: text })
      });
    } catch (e) {
      setState('the gateway refused the turn', true);
      finish();
      return;
    }
    // The response is the stream, and it is read line by line so the user sees the turn as it
    // happens rather than all at once at the end.
    follow();
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
        say('Open the link `starlight gateway start` printed: it carries the token once, in the ' +
            'part of the URL a browser never sends to the server.', 'agent kind');
        return;
      }
      setState('not connected', true);
    }
  }

  start();
})();
