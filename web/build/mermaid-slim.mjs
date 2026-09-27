// A Vite/Rollup plugin that keeps mermaid affordable to EMBED.
//
// mermaid's own `mermaid.core.mjs` carries around 37 diagram families behind
// `loader: () => import("./chunks/mermaid.core/xDiagram-*.mjs")`. A bundler emits
// every one of them whether or not it is ever loaded: measured, that is 5.3 MB of
// embedded bytes, plus elkjs (1.6 MB), cytoscape (1.1 MB) and the swimlane engine
// that the shared layout chunk pulls in for diagrams this project never draws.
// The interface has to live inside a Go binary with a hard size ceiling, so this
// keeps exactly the one family the project uses.
//
// Two transforms, both driven by CONTENT rather than by a content-hashed filename
// (the hashes change on every mermaid release and would silently stop matching):
//
//  1. `mermaid.core.mjs` - every inline dynamic import becomes a rejection, so no
//     other diagram family is emitted. The message says what to do about it.
//  2. the SHARED layout chunk - the one that registers four layout engines - keeps
//     mermaid's own `dagre` registration and drops the rest.
//
// The second one is the subtle half, and getting it wrong is silent: an EMPTY
// module for an engine still leaves it REGISTERED, so mermaid picks it and dies at
// render time with "i.render is not a function". `getRegisteredLayoutAlgorithm`
// only falls back to dagre when an algorithm is missing from the registry, so the
// entries have to be REMOVED. This mirrors the switch mermaid itself uses for its
// "tiny build" ("elkjs is ~1.6 MB of source, so it is excluded from the tiny build
// ... then falls back to dagre").
//
// Every anchor is asserted. A mermaid upgrade that changes any of this shape fails
// the BUILD with a pointer, instead of quietly shipping a five-megabyte page.
import fs from 'node:fs'
import path from 'node:path'

const ENGINE_RE = /import\("\.\/((?:elk|dagre|swimlanes|cose-bilkent)-[^"]+\.mjs)"\)/g
const LAZY_DIAGRAM_RE = /import\("\.\/chunks\/mermaid\.core\/[^"]+"\)/g

/** The directory mermaid's internal chunks live in, resolved from node_modules. */
function mermaidChunksDir(root) {
  const dist = path.join(root, 'node_modules/mermaid/dist')
  const chunks = path.join(dist, 'chunks/mermaid.core')
  if (!fs.existsSync(chunks)) {
    throw new Error(`mermaid-slim: ${chunks} does not exist. Is mermaid installed? (npm ci)`)
  }
  return chunks
}

/**
 * The layout chunk that registers the layout engines. Found by the engine imports
 * it CONTAINS, so it survives mermaid renaming the file.
 */
function sharedLayoutChunk(chunks) {
  const hits = fs
    .readdirSync(chunks)
    .filter((f) => f.endsWith('.mjs'))
    .filter((f) => [...fs.readFileSync(path.join(chunks, f), 'utf8').matchAll(ENGINE_RE)].length >= 2)
  if (hits.length !== 1) {
    throw new Error(
      `mermaid-slim: expected exactly one chunk registering several layout engines, found ${hits.length} ` +
        `(${hits.join(', ')}). mermaid changed how layouts are registered; the transform needs updating.`,
    )
  }
  return hits[0]
}

/** The flowchart chunk, which is the ONE diagram family this page renders. */
function flowchartChunk(chunks) {
  const hits = fs.readdirSync(chunks).filter((f) => /^flowDiagram-.*\.mjs$/.test(f))
  if (hits.length !== 1) {
    throw new Error(
      `mermaid-slim: expected exactly one flowDiagram chunk, found ${hits.length} (${hits.join(', ')}).`,
    )
  }
  return hits[0]
}

export default function mermaidSlim() {
  let root = process.cwd()
  let chunks = ''
  let shared = ''
  let flow = ''

  function prepare() {
    chunks = mermaidChunksDir(root)
    shared = path.join(chunks, sharedLayoutChunk(chunks))
    flow = path.join(chunks, flowchartChunk(chunks))
  }

  return {
    name: 'mermaid-slim',
    // Vite resolves project paths through `configResolved`; the plugin is also used
    // directly by the size probe, which sets `cwd` itself.
    configResolved(config) {
      root = config.root
      prepare()
    },

    resolveId(id) {
      if (id === 'virtual:mermaid-flowchart') return flow
      return null
    },

    transform(code, id) {
      const file = id.split('?')[0]

      // 1. Only the flowchart family is emitted.
      if (path.basename(file) === 'mermaid.core.mjs') {
        const before = [...code.matchAll(LAZY_DIAGRAM_RE)].length
        if (before === 0) {
          throw new Error(
            'mermaid-slim: mermaid.core.mjs no longer imports its diagram families inline, so nothing ' +
              'would be dropped. The transform needs updating before this build means anything.',
          )
        }
        return {
          code: code.replace(
            LAZY_DIAGRAM_RE,
            'Promise.reject(new Error("this diagram is not part of this build"))',
          ),
          map: null,
        }
      }

      // 2. The shared layout chunk keeps dagre and drops the engines nothing here uses.
      if (file === shared) {
        const entry = code.match(
          /\{\s*name: "dagre",\s*loader: \/\* @__PURE__ \*\/ __name\(async \(\) => await import\("\.\/(dagre-[^"]+\.mjs)"\), "loader"\)\s*\}/,
        )
        if (!entry) {
          throw new Error(
            'mermaid-slim: the dagre layout registration was not found in the shared layout chunk. ' +
              'mermaid changed how layouts are registered; the transform needs updating.',
          )
        }
        const list = /registerLayoutLoaders\(\[[\s\S]*?\]\);/
        if (!list.test(code)) {
          throw new Error('mermaid-slim: the layout loader list was not found in the shared layout chunk.')
        }
        const dropped = [...code.matchAll(ENGINE_RE)].map((m) => m[1]).filter((f) => f !== entry[1])
        if (dropped.length === 0) {
          throw new Error(
            'mermaid-slim: no layout engine was dropped, so the transform would do nothing. Either ' +
              'mermaid changed shape or the file found was not the shared layout chunk.',
          )
        }
        return { code: code.replace(list, `registerLayoutLoaders([${entry[0]}]);`), map: null }
      }

      return null
    },
  }
}

export { flowchartChunk, sharedLayoutChunk, mermaidChunksDir }
