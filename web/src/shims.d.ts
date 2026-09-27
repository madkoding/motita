// Type declarations for the Markdown-IT plugins that ship no `.d.ts`.
// (mermaid's own types come from the package; it is used through its main entry
// now that the diagram families are not hand-picked — see web/src/mermaid.ts.)

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

