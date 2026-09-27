// Type declarations for the two kinds of module TypeScript cannot see into:
// the markdown-it plugins that ship no `.d.ts`, and the virtual module the
// mermaid-slim build plugin resolves to.

declare module 'markdown-it-footnote' {
  import type { PluginSimple } from 'markdown-it'
  const plugin: PluginSimple
  export default plugin
}

declare module 'markdown-it-deflist' {
  import type { PluginSimple } from 'markdown-it'
  const plugin: PluginSimple
  export default plugin
}

declare module 'markdown-it-task-lists' {
  import type { PluginWithOptions } from 'markdown-it'
  interface TaskListOptions {
    /** Render the checkbox as enabled. Default false (a reader cannot tick it). */
    enabled?: boolean
    /** Wrap the item in a <label> so the whole line is the tap target. */
    label?: boolean
    /** Put the label after the checkbox rather than before it. */
    labelAfter?: boolean
  }
  const plugin: PluginWithOptions<TaskListOptions>
  export default plugin
}

// The mermaid-slim plugin rewrites mermaid so that only the flowchart family is
// bundled, and resolves this specifier to mermaid's own flowchart chunk. The
// chunk exports a `DiagramDefinition`; the app only ever passes it straight back
// to mermaid, so the shape it needs is spelled out here rather than imported from
// a deep path inside mermaid's dist/.
declare module 'virtual:mermaid-flowchart' {
  import type { ExternalDiagramDefinition } from 'mermaid'
  export const diagram: ExternalDiagramDefinition['diagram']
}

// mermaid's "core" build is the one the slim plugin transforms. The package maps
// its TYPES only for the main entry (`mermaid`), so the specifier the plugin
// rewrites needs its own declaration. It is structurally the same mermaid, and it
// has to be declared as a VALUE: `import type` would make the default export
// unusable as a runtime object, which is all this module ever does with it.
declare module 'mermaid/dist/mermaid.core.mjs' {
  const mermaid: (typeof import('mermaid'))['default']
  export default mermaid
}
