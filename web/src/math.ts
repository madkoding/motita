// Math typesetting, loaded on demand.
//
// KaTeX is 600 KB of source and this module is imported only when an answer
// actually contains a formula. The Markdown renderer recognizes the delimiters and
// leaves a placeholder; this turns it into markup after the message is mounted.
//
// KaTeX runs with `output: 'mathml'` — that is what keeps this page free of ~1 MB of
// math fonts. Measured in Chrome with no math font installed at all: the MathML
// output lays out integrals, fractions, matrices and `aligned` blocks correctly,
// while the HTML output depends on its own font files and collapses into misaligned
// text without them. The reader's system font does the work, which is the standard
// MathML trade.
//
// `katex.min.css` is deliberately NOT imported: it is mostly `@font-face` rules for
// that HTML output, and pulling it in emitted the whole font set (60 files, ~1 MB)
// into the page for glyphs this build never draws. The handful of rules MathML does
// need are in index.css with the rest of the styling.
import katex from 'katex'

/**
 * Typesets one formula, returning HTML.
 *
 * A malformed formula does NOT throw: `throwOnError: false` makes KaTeX render the
 * source in place with the reason attached, which is what a reader needs — an
 * answer with a broken formula should still show the rest of itself.
 *
 * `output: 'mathml'` also makes KaTeX include `<annotation encoding="application/x-tex">`
 * holding the LaTeX source, which is the accessible fallback. It has to go, and this
 * is the measurement that put it here: the sanitizer downstream drops the annotation
 * ELEMENT but keeps its TEXT — `FORBID_TAGS` does not help, and forbidding both
 * `annotation` and `annotation-xml` produced byte-identical output — so the reader was
 * shown the typeset formula with the raw TeX printed underneath it. Removing it here,
 * deliberately, is the difference between showing the formula and showing the formula
 * twice. The `<math>` element keeps its structure; only the fallback text is dropped.
 */
export function renderMath(tex: string, displayMode: boolean): string {
  const html = katex.renderToString(tex, {
    displayMode,
    throwOnError: false,
    output: 'mathml',
    strict: () => 'ignore',
  })

  // Parsed as a document fragment so the removal is by ELEMENT, not by string surgery.
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  for (const a of Array.from(tpl.content.querySelectorAll('annotation'))) a.remove()
  return tpl.innerHTML
}
