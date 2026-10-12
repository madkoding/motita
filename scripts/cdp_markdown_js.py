"""The scripts cdp_markdown.py runs inside the page, kept apart from the checks that read them.

Each one is evaluated as-is through the DevTools protocol; nothing here runs in Python.
"""

# What the page ended up holding, read from the DOM and from computed styles. Kept as
# one expression so the page is interrogated once, in a consistent state.
MEASURE = r"""
(() => {
  const box = [...document.querySelectorAll('.markdown-body')]
    .find(b => b.textContent.includes('Salto'));
  if (!box) return {error: 'the answer is not rendered at all'};
  const q = s => box.querySelectorAll(s).length;
  const text = box.textContent;

  // 1. two trailing spaces are a HARD LINE BREAK: a real <br>, and the paragraph is
  //    taller than one line.
  const p1 = [...box.querySelectorAll('p')].find(p => p.textContent.includes('Línea uno'));
  const brCount = p1 ? p1.querySelectorAll('br').length : 0;
  const p1Height = p1 ? Math.round(p1.getBoundingClientRect().height) : 0;

  // 2. the escaped asterisks stay literal. Asserted by TEXT, not by counting <em>:
  //    the answer legitimately has 14 other emphasis runs.
  const escapePara = [...box.querySelectorAll('p')]
    .find(p => p.textContent.includes('esto no es cursiva'));
  const escapeText = escapePara ? escapePara.textContent.trim() : null;
  const escapeHasEm = escapePara ? escapePara.querySelectorAll('em').length : -1;

  // 3. inline HTML reaches the DOM, and <mark> is actually painted.
  const markBg = (() => {
    const el = box.querySelector('mark');
    return el ? getComputedStyle(el).backgroundColor : null;
  })();

  // 4. footnotes: the reference links to a real target that exists.
  const fnRef = box.querySelector('a[href^="#fn"]');
  const fnTarget = fnRef ? box.querySelector(fnRef.getAttribute('href')) : null;
  const fnListed = q('section.footnotes li') > 0;

  // 5. definition list.
  const dl = box.querySelector('dl');
  const dt = dl ? (dl.querySelector('dt') || {}).textContent : null;
  const dd = dl ? (dl.querySelector('dd') || {}).textContent : null;

  // 6. MATH: real <math> elements, sized, and NO raw TeX left showing. This is the
  //    assertion that catches the orphaned annotation text: the element count alone
  //    would pass on a page that also printed the LaTeX underneath.
  const maths = [...box.querySelectorAll('math')];
  const mathGeom = maths.map(m => {
    const r = m.getBoundingClientRect();
    return {w: Math.round(r.width), h: Math.round(r.height),
            display: m.getAttribute('display') || 'inline'};
  });
  const blockMath = box.querySelector('math[display="block"]');
  const rawTex = ['\\int_0', '\\frac', '\\sqrt', '\\infty'].filter(t => text.includes(t));
  const annotationLeak = maths.some(m => /\\[a-zA-Z]+/.test(m.textContent));

  // 7. DIAGRAMS: every diagram on the page, drawn, with visible LABELS. The label
  //    text is the whole point: the SVG renders empty boxes without it, which every
  //    shape-based check passes. All four are checked, because the reason mermaid is
  //    the full build is that a reader can put ANY diagram type in an answer.
  const blocks = [...box.querySelectorAll('.mermaid-block')];
  const diagrams = blocks.map(b => {
    const s = b.querySelector('svg');
    const r = s ? s.getBoundingClientRect() : null;
    const lab = [...b.querySelectorAll('.nodeLabel')]
      .map(n => n.textContent.trim()).filter(Boolean);
    // A label can also be a plain SVG <text> (sequence, gantt, pie all draw theirs
    // that way), so the visible-ink test covers both rather than trusting one class.
    const texts = [...b.querySelectorAll('text')]
      .map(t => t.textContent.trim()).filter(Boolean);
    return {
      done: b.getAttribute('data-done'),
      svg: !!s, w: r ? Math.round(r.width) : 0, h: r ? Math.round(r.height) : 0,
      // The reason lives in .render-error-reason (see fail() in Markdown.tsx).
      error: b.classList.contains('render-error')
        ? ((b.querySelector('.render-error-reason') || {}).textContent || '').slice(0, 300)
          + ' || source: ' + ((b.querySelector('pre') || {}).textContent || '').slice(0, 80)
        : null,
      labels: lab, texts,
      edges: b.querySelectorAll('.edgePaths path, path.flowchart-link').length,
      arrowMarker: [...b.querySelectorAll('path')].some(p => p.getAttribute('marker-end')),
      actors: [...b.querySelectorAll('.actor')].map(a => a.textContent.trim()).filter(Boolean),
      tasks: [...b.querySelectorAll('.taskText, .sectionTitle')]
        .map(t => t.textContent.trim()).filter(Boolean),
      slices: [...b.querySelectorAll('.pieCircle, .slice')].length,
    };
  });
  const mm = blocks[0] || null;

  // 8. alerts render as callouts, not as blockquotes with the word NOTE inside.
  const alerts = [...box.querySelectorAll('.markdown-alert')].map(e => e.className);

  // 9. code: highlighted, and the copy payload is non-empty EVEN THOUGH the snippet
  //    contains `-->` — the case the sanitizer emptied.
  const codeBtn = box.querySelector('pre.code-block .copy-btn');
  const copyText = codeBtn ? codeBtn.getAttribute('data-copy-text') || '' : null;
  const token = box.querySelector('pre.code-block code span[class*=hljs]');
  const tokenColor = token ? getComputedStyle(token).color : null;
  const codeText = box.querySelector('pre.code-block code');

  // 10. the message's own copy payload, which contains `-->` too.
  const msgRaw = box.getAttribute('data-raw') || '';

  // 11. EMOJI: converted to images in the text, and NOT inside code. The second half is
  //    the assertion that matters: a conversion that runs over a code block would corrupt
  //    the snippet the reader copies, which is a defect worse than not having the feature.
  const emojiImgs = [...box.querySelectorAll('img.emoji')];
  const emojiInText = emojiImgs.filter(i => !i.closest('pre, code')).length;
  const emojiInCode = emojiImgs.filter(i => i.closest('pre, code')).length;
  const codeEmojiLeft = [...box.querySelectorAll('pre code')]
    .some(c => /[\u{1F300}-\u{1FAFF}]/u.test(c.textContent));
  const emojiGeom = emojiImgs.slice(0, 4).map(i => {
    const r = i.getBoundingClientRect();
    const cs = getComputedStyle(i);
    return {w: Math.round(r.width), h: Math.round(r.height), src: i.getAttribute('src'),
            alt: i.alt, loaded: i.complete && i.naturalWidth > 0,
            // A character, not a framed picture: the generic `.markdown-body img` rule styles
            // a screenshot, and any of its box properties reaching an emoji shows up as a
            // stray border with padding. The font size is carried alongside so the size can
            // be judged against the text rather than against a number that means nothing here.
            border: cs.borderTopWidth, radius: cs.borderTopLeftRadius, margin: cs.marginLeft,
            pad: cs.paddingTop, display: cs.display, va: cs.verticalAlign,
            font: cs.fontSize};
  });
  // hydrate() runs on the element the component rendered, which is the PARENT of
  // `.markdown-body`, so the marker is looked up from the box outwards rather than read off
  // the box: reading it off the box directly is how this reported "the pass did not run"
  // while fifteen emoji were already rendered as images.
  const emojiHost = box.closest('[data-emoji]') || box;
  const emojiState = emojiHost.getAttribute('data-emoji');
  const emojiCount = emojiHost.getAttribute('data-emoji-count') || box.getAttribute('data-emoji-count');
  // A ZWJ sequence (the rainbow flag, the black cat) is ONE picture, and a country flag has
  // no FluentUI asset at all: both must survive as their original characters rather than
  // becoming a series of images that shows half a sequence or a broken image.
  const emojiAlt = emojiImgs.map(i => i.alt);
  const textLeft = box.textContent || '';
  const flagLeft = textLeft.includes('🇨🇱');
  const zwjWhole = emojiAlt.includes('🏳️‍🌈') && emojiAlt.includes('🐈‍⬛')
  const zwjSplit = emojiAlt.some(a => a === '🏳' || a === '🌈' || a === '🐈' || a === '⬛')
  // A keycap (`1️⃣`) and a tag-sequence flag (Scotland) are both multi-codepoint too, and
  // both are cases where a lookup that misses looks identical to a character with no asset.
  const keycapOk = emojiAlt.includes('1️⃣') && emojiAlt.includes('2️⃣')
  const tagFlagLeft = textLeft.includes('\u{1F3F4}\u{E0067}\u{E0062}\u{E0073}\u{E0063}\u{E0074}\u{E007F}')
  const blackFlagShown = emojiAlt.includes('🏴')

  return {
    lineBreak: {br: brCount, paragraphHeight: p1Height},
    escape: {text: escapeText, emInside: escapeHasEm},
    inlineHtml: {kbd: q('kbd'), mark: q('mark'), sub: q('sub'), sup: q('sup'), markBg},
    footnotes: {refs: q('a[href^="#fn"]'), targetExists: !!fnTarget, listed: fnListed},
    deflist: {dl: !!dl, dt, dd},
    math: {count: maths.length, geom: mathGeom,
           blockVisible: blockMath ? blockMath.getBoundingClientRect().height > 0 : false,
           rawTexShowing: rawTex, annotationLeak},
    mermaid: {present: !!mm, svg: !!mm && !!mm.querySelector('svg'),
              w: mm && mm.querySelector('svg')
                 ? Math.round(mm.querySelector('svg').getBoundingClientRect().width) : 0,
              h: mm && mm.querySelector('svg')
                 ? Math.round(mm.querySelector('svg').getBoundingClientRect().height) : 0,
              done: mm ? mm.getAttribute('data-done') : null},
    diagrams,
    alerts,
    code: {copyTextLen: copyText === null ? null : copyText.length,
           copyText, tokenColor,
           codeLen: codeText ? codeText.textContent.length : 0},
    messageCopyLen: msgRaw.length,
    emoji: {state: emojiState, count: emojiCount, total: emojiImgs.length,
            inText: emojiInText, inCode: emojiInCode, codeEmojiLeft,
            alt: emojiAlt, flagLeft, zwjWhole, zwjSplit,
            keycapOk, tagFlagLeft, blackFlagShown,
            geom: emojiGeom},
    renderErrors: q('.render-error'),
  };
})()
"""

# A recorder that outlives the navigation it observes: it watches the conversation container
# from the first frame, so "was it hidden WHILE loading?" is answered from the page's own
# history instead of being inferred from a sample taken later.
RECORDER = """
            (() => {
              const log = [];
              window.__visLog = log;
              const frames = [];
              window.__frameLog = frames;
              let lastKey = null;
              const tick = () => {
                const el = document.querySelector('main[role="log"]');
                const scrim = document.querySelector('.chat-modal-scrim');
                const loading = el ? el.getAttribute('data-chat-loading') : null;
                // Per-FRAME, and every frame: a transition is many intermediate values, a jump is
                // one. Thinned by the key so the array stays small, but the key includes the
                // opacity so every intermediate step is kept.
                const key = (el ? getComputedStyle(el).opacity : '-') + '|' + loading + '|' +
                            (scrim ? getComputedStyle(scrim).opacity : '-');
                if (key !== lastKey) {
                  lastKey = key;
                  frames.push({
                    at: Math.round(performance.now()),
                    chat: el ? getComputedStyle(el).opacity : null,
                    loading: loading,
                    modal: scrim ? getComputedStyle(scrim).opacity : null,
                    scrollTop: el ? Math.round(el.scrollTop) : null,
                    scrollBehavior: el ? getComputedStyle(el).scrollBehavior : null,
                    msg: (() => {
                      const c = el && el.firstElementChild;
                      return c ? getComputedStyle(c).opacity : null;
                    })(),
                  });
                }
                if (el) {
                  const cs = getComputedStyle(el);
                  const stable = cs.opacity + '|' + loading + '|' + el.scrollHeight + '|' +
                                 el.scrollTop;
                  if (stable !== lastKey) {
                    // Everything that could move the scroll when the conversation changes size.
                    // Attributed per element so a resize is NAMED instead of guessed at.
                    const box = (n) => {
                      if (!n) return null;
                      const r = n.getBoundingClientRect();
                      return { h: Math.round(r.height), top: Math.round(r.top) };
                    };
                    const parent = el.parentElement;
                    log.push({
                      at: Math.round(performance.now()), opacity: cs.opacity,
                      loading: loading, modal: !!scrim,
                      contentHeight: el.scrollHeight,
                      boxHeight: Math.round(el.getBoundingClientRect().height),
                      scrollTop: Math.round(el.scrollTop),
                      clientHeight: el.clientHeight,
                      parent: box(parent),
                      parentClient: parent ? parent.clientHeight : null,
                      header: box(document.querySelector('header')),
                      aside: box(document.querySelector('aside')),
                      docHeight: document.documentElement.scrollHeight,
                      innerHeight: window.innerHeight,
                    });
                  }
                }
              };
              setInterval(tick, 16);
            })();
            """

# The observation of the chat spinner, installed BEFORE the conversation is opened.
#
# It watches the MUTATION RECORDS rather than re-querying the document, so a node that
# is added and removed between two callbacks is still seen. The description is taken
# from the live element whenever one is present, because `getComputedStyle` on a
# detached node reports nothing — which is what the colour check would read.
LOAD_LOG = r"""(() => {
          const log = window.__loadLog = { spinner: null, seen: 0, clearedAt: null,
                                            heights: [], samples: 0 };
          const describe = (wrap) => {
            const svg = wrap.querySelector('svg');
            const path = svg && svg.querySelector('path');
            const anim = path && path.querySelector('animateTransform');
            const r = svg ? svg.getBoundingClientRect() : null;
            return {
              label: (wrap.querySelector('.chat-modal-label') || {}).textContent || null,
              d: path ? path.getAttribute('d') : null,
              fill: path ? getComputedStyle(path).fill : null,
              w: r ? Math.round(r.width) : 0,
              h: r ? Math.round(r.height) : 0,
              rotate: anim ? anim.getAttribute('values') : null,
              dur: anim ? anim.getAttribute('dur') : null,
              repeat: anim ? anim.getAttribute('repeatCount') : null,
              role: (wrap.querySelector('.chat-modal') || wrap).getAttribute('role'),
              live: !!wrap.isConnected,
              // The card, and the area it sits in: a small modal is a much smaller box inside
              // the scrim, which is also how the two designs are told apart in a measurement.
              modal: (() => {
                const card = wrap.querySelector('.chat-modal');
                const cr = card ? card.getBoundingClientRect() : null;
                const sr = wrap.getBoundingClientRect();
                return {
                  w: cr ? Math.round(cr.width) : 0,
                  h: cr ? Math.round(cr.height) : 0,
                  scrimW: Math.round(sr.width),
                  scrimH: Math.round(sr.height),
                };
              })(),
            };
          };
          const note = (wrap) => {
            log.seen++;
            if (wrap.isConnected) log.spinner = describe(wrap);
          };
          const sample = () => {
            const m = document.querySelector('main[role="log"]');
            log.samples++;
            // Only once the conversation has content: the height before that says nothing
            // about whether the deferred work resized the page.
            if (m && document.querySelector('.markdown-body')) {
              log.heights.push(m.scrollHeight);
              // Height AND position: a gap that survives a scroll the reader is following is
              // either a scroll that did not land or a page that grew again afterwards, and the
              // two need opposite fixes. Recording both is what tells them apart.
              log.geom = log.geom || [];
              if (log.geom.length < 60) log.geom.push({
                t: Math.round(performance.now()),
                h: m.scrollHeight, top: Math.round(m.scrollTop), c: m.clientHeight,
                gap: m.scrollHeight - Math.round(m.scrollTop) - m.clientHeight,
              });
            }
          };
          // Sampled on a timer as well as on mutations: the growth that was stranding the reader
          // raised no event at all, so a mutation-driven sampler cannot see it.
          log.timer = setInterval(sample, 120);
          const io = window.__ioLog = { callbacks: 0, decisions: [] };
          // The real observer is wrapped so its callbacks are recorded: a `data-inview` of 1 is
          // otherwise indistinguishable from "the observer never reported and the default stuck".
          const RealIO = window.IntersectionObserver;
          window.IntersectionObserver = class extends RealIO {
            constructor(cb, opts) {
              super((records, ob) => {
                io.callbacks++;
                if (io.opts === undefined) io.opts = opts && {rootMargin: opts.rootMargin, threshold: opts.threshold};
                for (const r of records) {
                  const b = r.boundingClientRect, rb = r.rootBounds;
                  io.decisions.push({
                    hit: r.isIntersecting,
                    top: Math.round(b.top), h: Math.round(b.height),
                    rootTop: rb ? Math.round(rb.top) : null,
                    rootBottom: rb ? Math.round(rb.bottom) : null,
                  });
                }
                if (io.decisions.length > 40) io.decisions = io.decisions.slice(-40);
                cb(records, ob);
              }, opts);
            }
          };

          const obs = new MutationObserver((records) => {
            for (const rec of records) {
              for (const n of rec.addedNodes) {
                if (n.nodeType !== 1) continue;
                if (n.matches('.chat-modal-scrim')) { note(n); continue; }
                const inner = n.querySelector && n.querySelector('.chat-modal-scrim');
                if (inner) note(inner);
              }
              if (log.clearedAt === null) {
                for (const n of rec.removedNodes) {
                  if (n.nodeType !== 1) continue;
                  const gone = n.matches('.chat-modal-scrim') ||
                               (n.querySelector && n.querySelector('.chat-modal-scrim'));
                  if (gone && log.seen) {
                    log.clearedAt = performance.now();
                    const hosts = [...document.querySelectorAll('[data-hydrating]')];
                    const vis = hosts.filter(h => h.getAttribute('data-inview') !== '0');
                    log.atClear = {
                      hostsPending: hosts.length,
                      visiblePending: vis.length,
                      hydratedSoFar: (window.__idleLog || {}).hydratedCount || 0,
                    };
                  }
                }
              }
            }
            // A spinner still on screen is described again from the live node: the first
            // sighting may arrive in a batch where nothing has been laid out yet.
            const now = document.querySelector('.chat-modal-scrim');
            if (now) note(now);
            sample();
          });
          obs.observe(document.body, {
            childList: true, subtree: true, attributes: true,
            attributeFilter: ['data-hydrating'],
          });
          sample();
          // A second, tiny recorder for the timing question: did the modal come down only
          // AFTER the last message finished? `__loadLog` already times the clear; this counts
          // the completions and stamps the last one, plus when the page called itself idle.
          const idle = window.__idleLog = { hydratedCount: 0, lastHydratedAt: null, idleAt: null };
          document.addEventListener('motita:hydrated', () => {
            idle.hydratedCount++;
            idle.lastHydratedAt = performance.now();
          }, true);
          document.addEventListener('motita:chat-idle', () => {
            if (idle.idleAt === null) idle.idleAt = performance.now();
          });
          window.__loadLogStop = () => obs.disconnect();
          return true;
        })()"""
