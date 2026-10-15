// The reveal of a streamed answer: the text that arrives is shown letter by letter, at a steady
// pace, with a short fading edge, instead of appearing in whole chunks as the model sends them.
//
// It never touches the message's DOM. The answer is re-rendered as ONE innerHTML string on every
// chunk (Markdown.tsx), and its placeholders keep their payloads in text content for `hydrate`,
// so cutting text nodes the way the terminal's typewriter does would break both. Instead the
// characters not yet revealed are painted transparent with the CSS Custom Highlight API: the
// text is laid out in full from the start, so the bubble's height never jumps as letters appear
// and the scroll that follows the answer sees the real height. Where the API is missing the
// answer simply shows as it arrives, as it always did.
//
// Only text that GROWS after the message mounted is revealed: a message read from the history
// mounts complete and shows at once.

import { choose } from "./choose";

// The reveal keeps up with the stream: it never runs below MIN_CPS, and it speeds up so the
// backlog it is behind by is shown within CATCH_UP_S.
const MIN_CPS = 90;
const CATCH_UP_S = 0.6;
// The fading edge: FADE_BANDS groups of FADE_BAND characters, each more transparent than the last.
const FADE_BANDS = 3;
const FADE_BAND = 3;

// Highlight names, styled in index.css as `::highlight(stream-hidden)` and `stream-fade-N`.
const HIDDEN = "stream-hidden";
const FADE = (i: number) => "stream-fade-" + (i + 1);

interface HighlightLike {
  add(r: AbstractRange): void;
  delete(r: AbstractRange): boolean;
}

// highlights returns the registry's highlights, created on first use; null without the API.
function highlights(): HighlightLike[] | null {
  const reg = (globalThis as { CSS?: { highlights?: Map<string, HighlightLike> } }).CSS
    ?.highlights;
  const Ctor = (globalThis as { Highlight?: new () => HighlightLike }).Highlight;
  if (!reg || !Ctor) {
    return null;
  }
  return [HIDDEN, ...Array.from({ length: FADE_BANDS }, (_, i) => FADE(i))].map((name) => {
    let h = reg.get(name);
    if (!h) {
      h = new Ctor();
      reg.set(name, h);
    }
    return h;
  });
}

// textNodes lists the answer's text, in order, without the copy button's label.
function textNodes(root: HTMLElement): { nodes: Text[]; total: number } {
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {
    acceptNode: (n) =>
      choose(n.parentElement?.closest(".copy-msg-btn"), NodeFilter.FILTER_REJECT, NodeFilter.FILTER_ACCEPT),
  });
  const nodes: Text[] = [];
  let total = 0;
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    nodes.push(n as Text);
    total += (n as Text).data.length;
  }
  return { nodes, total };
}

// point finds the text node and offset of character `at`; past the end, the end of the last node.
function point(nodes: Text[], at: number): [Text, number] {
  let left = at;
  for (const n of nodes) {
    if (left <= n.data.length) {
      return [n, left];
    }
    left -= n.data.length;
  }
  const last = nodes[nodes.length - 1];
  return [last, last.data.length];
}

export interface StreamReveal {
  // update is called after the element's content was replaced, with the new source text.
  update(content: string): void;
  dispose(): void;
}

// streamReveal reveals `el`'s text as it grows. `content` is the source the element mounted with.
export function streamReveal(el: HTMLElement, content: string): StreamReveal {
  const hl = highlights();
  if (!hl) {
    return { update() {}, dispose() {} };
  }
  let source = content;
  // Characters revealed, counted over the RENDERED text. Everything mounted is revealed.
  let { nodes, total } = textNodes(el);
  let shown = total;
  let ranges: StaticRange[] = [];
  let raf = 0;
  let last = 0;
  // Hydration (code colours, formulas) replaces text nodes under the reveal: the list is read
  // again before the next paint, or the ranges would point at nodes no longer in the page.
  let stale = false;
  let observer: MutationObserver | null = null;
  if (typeof MutationObserver !== "undefined") {
    observer = new MutationObserver(() => {
      stale = true;
    });
  }
  observer?.observe(el, { childList: true, subtree: true, characterData: true });

  const clear = () => {
    ranges.forEach((r, i) => hl[i].delete(r));
    ranges = [];
  };

  // paint marks everything after `shown` hidden, and the characters just before it as the
  // fading edge, the newest the faintest.
  const paint = () => {
    clear();
    if (stale) {
      stale = false;
      ({ nodes, total } = textNodes(el));
      shown = Math.min(shown, total);
    }
    if (nodes.length === 0 || shown >= total) {
      return;
    }
    const edges = [shown];
    for (let i = 0; i < FADE_BANDS; i++) {
      edges.push(Math.max(0, shown - (i + 1) * FADE_BAND));
    }
    const make = (from: number, to: number) => {
      const [sn, so] = point(nodes, from);
      const [en, eo] = point(nodes, to);
      return new StaticRange({ startContainer: sn, startOffset: so, endContainer: en, endOffset: eo });
    };
    ranges.push(make(shown, total));
    for (let i = 0; i < FADE_BANDS; i++) {
      ranges.push(make(edges[i + 1], edges[i]));
    }
    ranges.forEach((r, i) => hl[i].add(r));
  };

  const step = (now: number) => {
    let dt = 0;
    if (last) {
      dt = (now - last) / 1000;
    }
    last = now;
    const rate = Math.max(MIN_CPS, (total - shown) / CATCH_UP_S);
    shown = Math.min(total, shown + rate * dt);
    paint();
    if (shown < total) {
      raf = requestAnimationFrame(step);
    } else {
      raf = 0;
    }
    if (!raf) {
      last = 0;
    }
  };

  return {
    update(next: string) {
      const grew = next.length > source.length && next.startsWith(source);
      source = next;
      const before = shown;
      ({ nodes, total } = textNodes(el));
      stale = false;
      if (!grew) {
        // Anything other than more of the same answer (an edit, a reload) shows at once.
        shown = total;
        paint();
        return;
      }
      shown = Math.min(before, total);
      paint();
      if (!raf && shown < total) {
        raf = requestAnimationFrame(step);
      }
    },
    dispose() {
      observer?.disconnect();
      cancelAnimationFrame(raf);
      raf = 0;
      clear();
    },
  };
}
