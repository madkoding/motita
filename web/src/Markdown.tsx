// The Markdown the agent's answers are rendered with.
//
// This replaced a hand-written, dependency-free renderer that had grown to cover
// headings, emphasis, code, lists, tables, footnotes and alerts on its own. It was
// 423 lines of regex and it could not get the corner cases right: measured on a
// document that exercises the CommonMark spec plus the usual extensions, it left
// TWO TRAILING SPACES (a hard line break) as text, it turned `\*not italic\*` into
// an emphasis with a visible backslash, and it had no idea what math or a diagram
// was. Three things it claimed to support had never worked:
//
//   * tables with alignment (`|:---|:---:|` parsed as plain cells);
//   * autolinks (`<https://x>` stayed as escaped text);
//   * images, which were only allowed from `data:` URLs and dropped otherwise.
//
// So the parsing is now a real CommonMark implementation with the GFM extensions,
// and this file is the part that is actually ours: WHICH extensions, what the
// output is allowed to contain, and the three things the old renderer got right
// that must keep working (the copy button, the raw-text attribute it copies from,
// and running while an answer is still streaming).
//
// ## Math, code colours and diagrams are DEFERRED, and that is the design
//
// The parse produces a skeleton of inert placeholders; the three expensive halves
// are filled in afterwards, and each of their modules is imported only when a
// message actually contains one. This is not a micro-optimization: the page is
// embedded in a Go binary under a hard budget and those three are well over a
// megabyte of source between them. Measured, with them imported eagerly, the shell
// alone was 486 KB of JavaScript.
//
// ## Every payload travels as TEXT, never as an attribute — and that is load-bearing
//
// A placeholder has to carry the source it will be rendered from, and the obvious
// place is a `data-` attribute. DOMPurify removes exactly that one, and the reason is
// worth stating because it looks like a bug and is not:
//
//   `SAFE_FOR_XML` (on by default) drops any attribute whose VALUE contains `<!--`,
//   `-->` or `<![CDATA[` — an HTML comment marker inside an attribute value is how an
//   mXSS payload escapes a sanitizer. Measured against this build: `<span data-math=
//   "A --> B">` comes back as `<span>` with no attribute at all, while the same
//   string as TEXT CONTENT survives untouched.
//
// Every mermaid edge is written `-->`. So the first version of this file shipped a
// diagram placeholder whose source attribute had been silently deleted, mermaid
// received the empty string, and the reader got "no diagram type detected" instead of
// their flowchart. The same filter would have emptied the attribute of a formula or a
// code block containing `-->`, and of `data-raw` — which is the copy button's payload,
// so copying a JavaScript answer would have produced an empty clipboard.
//
// Disabling `SAFE_FOR_XML` to make the attributes stick was measured and rejected: it
// is a real weakening, not a formality. `<noscript><p title="</noscript><img src=x
// onerror=alert(1)>">` comes back with the handler intact in the title attribute.
//
// So the payloads live in text content, and `hydrate` puts the attributes back AFTER
// the sanitizer has run — `setAttribute` assigns a value, it does not parse HTML, so
// nothing is reintroduced. The parse and the copy button keep the contract they had.
//
// ## `html: true` is deliberate, and it is why nothing reaches the DOM unsanitized
//
// Inline HTML in an answer renders (`<kbd>`, `<sup>`, `<details>`), which the old
// renderer escaped. That widening is only safe because every fragment — the parsed
// message, a KaTeX render and a mermaid SVG alike — goes through DOMPurify first.
import { useEffect, useRef } from 'preact/hooks'
import type { Config as PurifyConfig } from 'dompurify'
import MarkdownIt from 'markdown-it'
import type { MarkdownItOptions, Token } from 'markdown-it'
import DOMPurify from 'dompurify'
import { watchForHydration } from './hydration'
import { t } from './i18n'
import footnote from 'markdown-it-footnote'
import deflist from 'markdown-it-deflist'
import taskLists from 'markdown-it-task-lists'
import alerts from 'markdown-it-github-alerts'

/** Escapes a string for use inside a double-quoted attribute. */
function escapeAttr(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/**
 * The parser. Options are explicit rather than defaulted because two of them change
 * what a reader sees:
 *
 *   breaks: false  - CommonMark: one newline is NOT a line break, two trailing spaces
 *                    are. `breaks: true` would silently redefine every multi-line
 *                    answer, and the old renderer followed CommonMark here.
 *   linkify: false - `<https://x>` autolinks (CommonMark) work; bare `www.x` in prose
 *                    does not become a link, which is what the old renderer did and
 *                    the less surprising behaviour in a transcript.
 */
const md = new MarkdownIt({ html: true, breaks: false, linkify: false, typographer: false })
  .use(footnote)
  .use(deflist)
  .use(taskLists, { label: true })
  .use(alerts)

/**
 * `$$ ... $$` as a display formula.
 *
 * markdown-it has no rule for this, so the block is taken here rather than left to
 * fall through to the paragraph rule — which is what happened before: the closing `$$`
 * and the formula underneath it came out as ordinary text inside a `<p>`.
 *
 * The rule runs at line START only. A `$$` that opens mid-sentence is left alone,
 * because a mid-line `$$` is far more likely to be money or an escaped pair than a
 * display formula, and guessing wrong would eat the reader's text.
 */
md.block.ruler.before(
  'fence',
  'math_block',
  (state, startLine, endLine, silent): boolean => {
    const start = state.bMarks[startLine] + state.tShift[startLine]
    const max = state.eMarks[startLine]
    if (state.src.slice(start, start + 2) !== '$$') return false

    const afterOpen = state.src.slice(start + 2, max)
    let content: string
    let nextLine: number

    const sameLine = afterOpen.indexOf('$$')
    if (sameLine >= 0) {
      // `$$ x^2 $$` on one line.
      content = afterOpen.slice(0, sameLine)
      nextLine = startLine + 1
    } else {
      // The opening `$$` may have the formula on it, or nothing at all.
      const lines: string[] = afterOpen.trim() === '' ? [] : [afterOpen]
      let closed = false
      let line = startLine + 1
      for (; line < endLine; line++) {
        const ls = state.bMarks[line] + state.tShift[line]
        const le = state.eMarks[line]
        const text = state.src.slice(ls, le)
        const close = text.indexOf('$$')
        if (close >= 0) {
          const head = text.slice(0, close)
          if (head.trim() !== '') lines.push(head)
          closed = true
          break
        }
        lines.push(text)
      }
      // An unclosed `$$` is not a formula: it stays as the reader wrote it.
      if (!closed) return false
      content = lines.join('\n')
      nextLine = line + 1
    }

    if (silent) return true
    const token = state.push('math_block', 'math', 0)
    token.block = true
    token.content = content.trim()
    token.map = [startLine, nextLine]
    state.line = nextLine
    return true
  },
  { alt: ['paragraph', 'reference', 'blockquote', 'list'] },
)

// A fence is one of four things, and none of them can be finished here: a diagram
// and a formula need a module that may not be loaded yet, and code needs one that
// should not be loaded unless there is code.
//
// Each placeholder carries its source as TEXT (see the note at the top of the file)
// and only safe flags as attributes.
md.renderer.rules.fence = (tokens: Token[], idx: number): string => {
  const token = tokens[idx]
  const info = (token.info || '').trim().split(/\s+/)[0].toLowerCase()
  const raw = token.content.replace(/\n$/, '')

  if (info === 'mermaid') {
    // Inert: `hydrate` fills it in. Whether the source is a valid diagram is decided
    // at RENDER time, never here.
    return '<div class="mermaid-block">' + md.utils.escapeHtml(raw) + '</div>\n'
  }
  if (info === 'math') {
    return '<div class="math-block" data-display="1">' + md.utils.escapeHtml(raw) + '</div>\n'
  }

  // Code, emitted PLAIN and coloured later. The raw text is the <code> element's own
  // content, and `hydrate` copies it into the `data-copy-text` attribute the delegated
  // click handler in App.tsx reads.
  return (
    '<pre class="code-block" data-code-lang="' +
    escapeAttr(info) +
    '"><button class="copy-btn">' + md.utils.escapeHtml(t('copy')) + '</button><code' +
    (info ? ' class="' + escapeAttr('lang-' + info) + ' hljs"' : ' class="hljs"') +
    '>' +
    md.utils.escapeHtml(raw) +
    '</code></pre>\n'
  )
}

// `$$` handled at block level above; the renderer for it.
md.renderer.rules.math_block = (tokens: Token[], idx: number): string =>
  '<div class="math-block" data-display="1">' + md.utils.escapeHtml(tokens[idx].content) + '</div>\n'

/**
 * Inline math: `$...$`.
 *
 * Taken from the TEXT token, because markdown-it has no inline rule for it. The two
 * guards are what keep prose intact: an opening `$` followed by whitespace is money
 * rather than a formula ("$5 and $10"), and a `$` that is never closed stays as it was
 * written. The closing `$` may not be followed by another one, so `$$b$$` is not read
 * as a formula between stray dollars.
 */
const INLINE_MATH = /\$([^\s$][^$\n]*?)\$(?!\$)/g
const MATH_OPEN = '<span class="math-inline">'
const MATH_CLOSE = '</span>'

const defaultText =
  md.renderer.rules.text || ((tokens: Token[], idx: number) => md.utils.escapeHtml(tokens[idx].content))
md.renderer.rules.text = (
  tokens: Token[],
  idx: number,
  options: Required<MarkdownItOptions>,
  env,
  self,
): string => {
  const token = tokens[idx]
  if (!token.content.includes('$')) return defaultText(tokens, idx, options, env, self)

  // Escaped gap by gap, around the placeholders that are inserted verbatim. Walking
  // the matches explicitly is what keeps this honest: the alternative (replace, then
  // split on the inserted markup) has to reconstruct where the real HTML was, and a
  // formula whose TeX happens to contain the placeholder text escapes that.
  let out = ''
  let last = 0
  for (const m of token.content.matchAll(INLINE_MATH)) {
    out += md.utils.escapeHtml(token.content.slice(last, m.index))
    out += MATH_OPEN + md.utils.escapeHtml(m[1]) + MATH_CLOSE
    last = m.index + m[0].length
  }
  return out + md.utils.escapeHtml(token.content.slice(last))
}

// Built once, not per render: this runs for every message on every update.
//
// `ADD_ATTR: ['style']` is the deliberate part. Mermaid lays a diagram out with inline
// `style` attributes — an edge's `stroke-dasharray`, the arrow marker's transform — and
// DOMPurify strips them by default, so without this the diagram renders as unconnected
// boxes. It is also the attribute DOMPurify is most careful about, so it is written
// here on purpose rather than inherited by accident.
//
// `foreignObject` + `HTML_INTEGRATION_POINTS` is what makes a diagram's LABELS appear.
// With `htmlLabels: true` mermaid puts each node's text inside a `foreignObject` in the
// XHTML namespace, and DOMPurify drops both by default: the SVG kept its boxes, edges
// and markers and showed nothing written in them. It has to be BOTH settings — the
// element alone comes back empty, because the integration point is what tells DOMPurify
// to keep parsing the XHTML inside it.
//
// This is the one place the sanitizer is widened, so it was measured against a matrix
// of attack vectors rather than assumed: script elements, `img onerror`, `iframe
// src=javascript:`, `svg onload`, `a href=javascript:`, `form action`, `meta refresh`,
// `object data`, and two `mglyph`/`annotation-xml` mXSS breakouts all come back inert
// (0 of 12 leak a live handler or a surviving script element). The residual is a
// `style` attribute whose value reads `url(javascript:...)`, which is inert in every
// browser this page supports — `javascript:` in a CSS url() was an IE-only behaviour —
// and which `ALLOW_DATA_ATTR`/`ADD_ATTR: ['style']` admits for the diagram's geometry
// in the first place.
//
// `SAFE_FOR_XML` is left ON (the default). Turning it off would let the placeholder
// attributes carry their payloads directly, and it was measured: it also lets
// `<noscript><p title="</noscript><img src=x onerror=alert(1)>">` through with its
// handler intact. The payloads are text and the attributes are restored afterwards for
// exactly this reason.
const PURIFY_CONFIG: PurifyConfig = {
  USE_PROFILES: { html: true, svg: true, svgFilters: true, mathMl: true },
  ADD_ATTR: ['style'],
  ALLOW_DATA_ATTR: true,
  // The diagram's node labels live in an XHTML island inside the SVG.
  ADD_TAGS: ['foreignObject'],
  HTML_INTEGRATION_POINTS: { foreignobject: true },
}

/** Sanitizes rendered HTML, keeping math and diagrams intact. */
export function sanitize(html: string): string {
  return DOMPurify.sanitize(html, PURIFY_CONFIG)
}

// --- The deferred half --------------------------------------------------------

/** The source a placeholder was left carrying, as TEXT. */
function sourceOf(el: HTMLElement): string {
  return (el.querySelector('code')?.textContent ?? el.textContent ?? '').replace(/\n$/, '')
}

/**
 * Fills in everything the parse left as a placeholder.
 *
 * Every step is guarded by a `data-done` marker rather than a local variable,
 * because a streaming answer re-renders this whole subtree on every chunk: without
 * the marker the same formula would be re-typeset and the same diagram redrawn on
 * every token that arrives.
 *
 * The attributes the UI depends on are (re)assigned here, from the DOM, AFTER the
 * sanitizer has had its pass. `setAttribute` assigns a value rather than parsing HTML,
 * so this reintroduces nothing — and it is the only way the copy button can see an
 * answer that contains a comment marker.
 */
async function hydrate(root: HTMLElement): Promise<void> {
  const math = Array.from(root.querySelectorAll<HTMLElement>('.math-inline:not([data-done]), .math-block:not([data-done])'))
  const code = Array.from(root.querySelectorAll<HTMLElement>('pre.code-block:not([data-done])'))
  const diagrams = Array.from(root.querySelectorAll<HTMLElement>('.mermaid-block:not([data-done])'))
  // Emoji is the one step that is not a placeholder: it rewrites text that is already
  // rendered, so its marker goes on the ROOT and it runs on every mount that has not had it.
  const wantsEmoji = root.getAttribute('data-emoji') !== 'ok'
  if (math.length + code.length + diagrams.length === 0 && !wantsEmoji) return

  // Marked BEFORE any await: two effects can overlap while a module loads, and an
  // unmarked element would be picked up by both.
  for (const el of [...math, ...code, ...diagrams]) el.setAttribute('data-done', 'pending')

  if (code.length > 0) {
    // The copy payload is restored FIRST, before the highlighter replaces the code
    // element's content: it has to be the pristine source either way, and reading it
    // after highlighting would depend on what the grammar chose to wrap.
    for (const el of code) {
      el.querySelector('.copy-btn')?.setAttribute('data-copy-text', sourceOf(el))
    }
  }

  if (code.length > 0) {
    try {
      const { highlight } = await import('./highlight')
      for (const el of code) {
        const target = el.querySelector('code')
        if (!target) continue
        // The highlighter does its OWN escaping, which is what keeps the code shown and
        // the code copied byte-identical: the copy payload is the pristine source
        // restored above, and the highlighted text is escaped once, from that source.
        target.innerHTML = highlight(sourceOf(el), el.getAttribute('data-code-lang') || '')
        el.setAttribute('data-done', 'ok')
      }
    } catch (err) {
      // Uncoloured code is still correct code: nothing is shown to the reader.
      for (const el of code) el.setAttribute('data-done', 'plain')
      console.error('motita: syntax highlighting is unavailable:', err)
    }
  }
  if (math.length > 0) {
    try {
      const { renderMath } = await import('./math')
      for (const el of math) {
        // Written back so `fail` and any styling can read it; the DOM is the source.
        const tex = (el.getAttribute('data-math') || el.textContent || '').trim()
        el.setAttribute('data-math', tex)
        el.innerHTML = sanitize(renderMath(tex, el.getAttribute('data-display') === '1'))
        el.setAttribute('data-done', 'ok')
      }
    } catch (err) {
      for (const el of math) fail(el, t('the math renderer could not be loaded'), err)
    }
  }

  if (diagrams.length > 0) {
    try {
      const { renderMermaid } = await import('./mermaid')
      for (const el of diagrams) {
        const source = sourceOf(el)
        el.setAttribute('data-mermaid', source)
        try {
          el.innerHTML = sanitize(await renderMermaid(source))
          el.setAttribute('data-done', 'ok')
        } catch (err) {
          fail(el, t('this diagram could not be drawn'), err)
        }
      }
    } catch (err) {
      for (const el of diagrams) fail(el, t('the diagram renderer could not be loaded'), err)
    }
  }

  // LAST, and after every await: the emoji pass rewrites text nodes, and doing it before
  // the math and diagram placeholders were replaced would convert inside content that is
  // about to be thrown away (and, for a formula, inside LaTeX source).
  if (wantsEmoji) {
    root.setAttribute('data-emoji', 'ok')
    try {
      const { renderEmoji } = await import('./emoji')
      root.setAttribute('data-emoji-count', String(renderEmoji(root)))
    } catch (err) {
      // An emoji that stays a character is exactly as readable as before this existed.
      root.setAttribute('data-emoji', 'plain')
      console.error('motita: the emoji images are unavailable:', err)
    }
  }
}

/**
 * Shows what could not be rendered as its source plus the reason.
 *
 * An empty box would be worse than showing nothing: the reader would not know
 * whether the answer was wrong or the page was.
 */
function fail(el: HTMLElement, what: string, err: unknown): void {
  const source = el.getAttribute('data-math') || el.getAttribute('data-mermaid') || ''
  el.setAttribute('data-done', 'failed')
  el.classList.add('render-error')
  el.innerHTML =
    '<p>' +
    escapeAttr(what) +
    '.</p><pre>' +
    escapeAttr(source) +
    '</pre><p class="render-error-reason">' +
    escapeAttr(err instanceof Error ? err.message : String(err)) +
    '</p>'
}

/**
 * The full HTML for one message, sanitized.
 *
 * Returned as ONE string, which is what makes an answer cheap to re-render while it
 * streams: there is no virtual DOM diff of every token. The placeholders it contains
 * are filled in by `hydrate`, from the mounted element. The message's own copy payload
 * is set by the component below, after this string is sanitized.
 */
export function renderMessage(source: string): string {
  return sanitize(
    '<div class="markdown-body">' + '<button class="copy-msg-btn">' + md.utils.escapeHtml(t('copy')) + '</button>' + md.render(source) + '</div>',
  )
}

export function Markdown({ content }: { content: string }) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = ref.current
    if (!el) return
    // The copy button's payload, restored after the sanitizer has run: an answer
    // containing an HTML comment marker or an arrow would otherwise have had this
    // attribute dropped, and the button would copy an empty string.
    el.querySelector('.markdown-body')?.setAttribute('data-raw', content)

    // The expensive half — formulas, diagrams, code colours, emoji — is DEFERRED until this
    // message is about to be read. Everything stays in the DOM either way: the message is
    // parsed and mounted immediately, so the reader can select the whole conversation as one
    // document. Only the building is postponed, which is what keeps a conversation of a
    // hundred turns from rendering a hundred diagrams nobody has scrolled to.
    //
    // Each of those steps changes the height of the message, and nothing in the component
    // state changes while it happens, so a scroll that reacts to state alone stops wherever it
    // was and the page ends up short of the bottom. Announcing the end of the work is what lets
    // the container follow it — fired on the element so the listener stays scoped to this
    // message's subtree, and bubbling so it reaches the conversation container.
    return watchForHydration(el, () => {
      return hydrate(el).then(
        () => el.dispatchEvent(new CustomEvent('motita:hydrated', { bubbles: true })),
        () => el.dispatchEvent(new CustomEvent('motita:hydrated', { bubbles: true })),
      )
    })
  }, [content])

  return <div ref={ref} dangerouslySetInnerHTML={{ __html: renderMessage(content) }} />
}
