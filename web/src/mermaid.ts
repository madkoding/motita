// The mermaid side of the Markdown renderer, split out so it can be imported
// LAZILY: this module and everything it drags in is only fetched when an answer
// actually contains a diagram, and a reader who never sees one never downloads it.
//
// ## Why this is the FULL mermaid, and not a trimmed one
//
// An earlier version of this file registered only the flowchart family to keep the
// chunk small, and it did — 698 KB instead of 5.3 MB. That was still a bad trade, and
// it is worth recording why rather than re-litigating it: this renderer exists so an
// answer can show what it means, and "that diagram type is not supported" is a defect
// the reader cannot work around. A gantt chart, a sequence diagram or an ER model is
// exactly the sort of thing an answer contains, and half a renderer that fails on
// them is worse than none.
//
// The cost is bounded by the fact that it is LAZY. The whole library is ~5.3 MB of
// source and ~1.7 MB transferred, and it is fetched when — and only when — a message
// contains a diagram. The page's shell budget is untouched, and a reader who never
// sees a diagram pays nothing for this.
//
// So this module deliberately does NOT hand-pick diagram types: every family mermaid
// registers by itself is available — flowchart, sequence, class, state, ER, gantt,
// pie, journey, gitGraph, mindmap, timeline, quadrantChart, sankey, block, packet,
// architecture, radar, treemap, xychart and the rest.
import mermaid from 'mermaid'

// The diagram id mermaid wants for each render. It is a DOM id inside the SVG, so
// it must be unique per render and must not start with a digit; a counter is
// enough and is cheaper (and more predictable) than a random id.
let seq = 0

let ready: Promise<void> | null = null

/**
 * Initializes mermaid once, on first use.
 *
 * `securityLevel: 'strict'` is the important one: a diagram's labels can contain
 * links and HTML, and 'strict' is what keeps them from being trusted. The diagram
 * source arrives from a model, so it is untrusted input by definition.
 *
 * `htmlLabels` MUST be set at the TOP LEVEL of this config, which is the opposite of
 * where it looks like it belongs. Mermaid also accepts it under `flowchart:` — and that
 * nested key is NOT the one this version reads, so putting it there alone does nothing.
 * Measured on the plain `graph LR / A[Inicio] --> B[Fin]`, setting only the nested key
 * produced an SVG with two boxes, a connecting arrow, and NO text in it; only the
 * top-level key drew the labels.
 *
 * It has to be ON, too. With it off the label group comes back holding an empty `<rect>`
 * where the text should be, and the cause is in mermaid's own source, where the
 * markdown-text path has an explicitly empty branch:
 *
 *     if (markdownAutoWrap === false) {   // TODO: Disabling `markdownAutoWrap` is
 *     }                                   // currently broken for `htmlLabels: false`
 *
 * `markdownAutoWrap` defaults to true, so that path drops the label before the DOM ever
 * sees it. ON routes the label through `foreignObject` instead, which draws it — and
 * that is why `sanitize` in Markdown.tsx lets that one element through. BOTH halves are
 * needed: measured, turning the sanitizer's widening off while leaving this ON made the
 * labels vanish again, so neither change is the fix on its own.
 */
function ensureReady(): Promise<void> {
  if (!ready) {
    ready = (async () => {
      mermaid.initialize({
        startOnLoad: false,
        // A model-authored label must never become a live click target or a script.
        securityLevel: 'strict',
        theme: 'dark',
        // The page's own type scale; mermaid's default is sized for a full-page doc.
        fontFamily: 'inherit',
        htmlLabels: true,
        flowchart: { useMaxWidth: true },
      })
    })()
  }
  return ready
}

/**
 * Renders one diagram source to SVG.
 *
 * Throws if the source is not a valid diagram; the caller decides how to show that,
 * because an unparseable diagram is information the reader needs rather than a
 * failure to swallow.
 */
export async function renderMermaid(source: string): Promise<string> {
  await ensureReady()
  seq += 1
  const { svg } = await mermaid.render('motita-diagram-' + seq, source)
  return svg
}
