// The mermaid side of the Markdown renderer, split out so it can be imported
// LAZILY: this module and everything it drags in (about 1.1 MB) is only fetched
// when an answer actually contains a diagram, and a reader who never sees one
// never downloads it.
//
// Only the FLOWCHART family is bundled. `web/build/mermaid-slim.mjs` explains why
// and how: the honest summary is that full mermaid is 5.3 MB of embedded bytes,
// the project has a hard binary ceiling, and this is the one diagram type the
// project's own documentation uses.
import mermaid from 'mermaid/dist/mermaid.core.mjs'
// Resolved by the mermaid-slim build plugin to mermaid's own flowchart chunk.
// Deep-path import on purpose: this IS the wiring the plugin keeps alive.
import { diagram } from 'virtual:mermaid-flowchart'

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
      await mermaid.registerExternalDiagrams(
        [
          {
            id: 'flowchart-v2',
            detector: (text: string) => /^\s*(graph|flowchart)\b/.test(text),
            // mermaid destructures { id, diagram } out of what the loader resolves
            // to, and its own chunk exports `diagram` without an id, so the loader
            // supplies both halves: hand it the bare diagram and mermaid dies
            // reading `.styles` off undefined.
            loader: async () => ({ id: 'flowchart-v2', diagram }),
          },
        ],
        { lazyLoad: true },
      )
    })()
  }
  return ready
}

/**
 * Renders one diagram source to SVG.
 *
 * Throws if the source is not a valid diagram the bundle can draw; the caller
 * decides how to show that, because an unparseable diagram is information the
 * reader needs rather than a failure to swallow.
 */
export async function renderMermaid(source: string): Promise<string> {
  await ensureReady()
  seq += 1
  const { svg } = await mermaid.render('motita-diagram-' + seq, source)
  return svg
}
